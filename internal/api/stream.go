package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

const (
	// StreamCoalesce is how long a board's changes are gathered into one board frame.
	StreamCoalesce = 100 * time.Millisecond
	// StreamPing is the keepalive: a named event, since EventSource never surfaces a comment.
	StreamPing = 20 * time.Second
	// ReplayCap is the most change-log rows a reconnect reads after its cursor. Past it the stream
	// answers resync and the client re-fetches the board: no (project_id, id) index exists (P-148),
	// so a project's catch-up reads every row after the cursor, and the cursor is the client's.
	ReplayCap = 5000
	// streamBuffer is the stream's bus subscription buffer; a change that does not fit is dropped,
	// which costs nothing here: every board frame is a whole snapshot.
	streamBuffer = 256
	// boardTasksLimit is the tasks a board frame carries, as ui-backend's board hub reads them.
	boardTasksLimit = 200
	// replayPage is one ReplayAll read: the change log's largest page.
	replayPage = 1000
)

// Presence is the device session directory as the stream's presence frames read it (R0.25's
// directory; R1.18 wires the real one): its live entries, and a channel closed on the next
// register or unregister.
type Presence interface {
	Entries() []PresenceEntry
	Changed() <-chan struct{}
}

// PresenceEntry is one live CLI client process on this device, as R0.25's directory lists it.
type PresenceEntry struct {
	PID          int64  `json:"pid"`
	Role         string `json:"role"`
	IdentityID   string `json:"identityId"`
	AgentID      string `json:"agentId"`
	Adapter      string `json:"adapter,omitzero"`
	RegisteredAt string `json:"registeredAt"`
}

// boardFrame is ui-backend's board frame (board-hub's composition): get_board's answer, a page of
// the board's tasks and its feed, which the SPA writes straight into its three caches.
type boardFrame struct {
	Board service.GetBoardResp  `json:"board"`
	Tasks service.ListTasksResp `json:"tasks"`
	Feed  service.BoardFeedResp `json:"feed"`
}

type updatedFrame struct {
	URI   string `json:"uri"`
	Count int    `json:"count"`
}

type presenceFrame struct {
	Sessions []PresenceEntry `json:"sessions"`
}

// feedURI is conductor's feedUri: the board feed resource the updated frame names.
func feedURI(projectID, boardID string) string {
	return "batondeck://" + projectID + "/board/" + boardID + "/feed"
}

// stream is GET /api/stream?projectId&boardId (app.ts:3540): SSE with ready, ping every
// StreamPing, and, when a board is named, a board frame and an updated frame per StreamCoalesce of
// the board's changes, fed by the bus's project subscription; presence frames from the directory.
// A board stream is authorized by a board snapshot as the caller before any byte is written (a
// non-member gets 403, an unknown board 404), and again at every board frame: a caller who has lost
// the board has the stream closed. Each board frame's SSE id is the last change-log id it covers;
// a reconnect with that cursor (Last-Event-ID, or ?cursor=) replays what it missed as one board
// frame, or, past ReplayCap rows, gets resync. Notifications frames wait for a notifications
// service (R2.3).
type stream struct {
	o         *Options
	coalesce  time.Duration
	ping      time.Duration
	replayCap int
}

func newStream(o *Options) http.Handler {
	return &stream{o: o, coalesce: cmp.Or(o.Coalesce, StreamCoalesce), ping: cmp.Or(o.Ping, StreamPing), replayCap: cmp.Or(o.ReplayCap, ReplayCap)}
}

// change is what the stream keeps of a change-log row.
type change struct {
	id      int64
	boardID string // "" for a row naming only the project
}

func changeOf(id int64, payload []byte) change {
	var p struct {
		BoardID string `json:"boardId"`
	}
	_ = json.Unmarshal(payload, &p)
	return change{id: id, boardID: p.BoardID}
}

func (s *stream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := s.o.logger()
	caller, ok := auth.CallerFrom(r.Context())
	if !ok {
		auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeServiceError(w, fmt.Errorf("api: the response writer cannot flush"), logger)
		return
	}
	q := r.URL.Query()
	projectID, boardID := q.Get("projectId"), q.Get("boardId")
	if (projectID == "") != (boardID == "") {
		writeServiceError(w, domain.Validation("projectId and boardId go together", map[string]any{"field": "boardId"}), logger)
		return
	}
	scoped := projectID != ""
	ctx := r.Context()
	since, resume, err := resumeCursor(r)
	if err != nil {
		writeServiceError(w, err, logger)
		return
	}
	var live <-chan change
	if scoped {
		if _, err := s.o.Services.Feed.BoardSnapshot(ctx, caller, service.BoardSnapshotReq{ProjectID: projectID, BoardID: boardID}); err != nil {
			writeServiceError(w, err, logger)
			return
		}
		if s.o.Bus == nil {
			writeServiceError(w, fmt.Errorf("api: /api/stream has no bus"), logger)
			return
		}
		// Subscribed before the replay, so nothing falls between them.
		sub, err := s.o.Bus.SubscribeProjects(ctx, []string{projectID}, streamBuffer)
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		defer sub.Close()
		live = rows(ctx, sub)
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	out := &sse{w: w, f: flusher}
	out.event("ready", 0, struct{}{})

	pending, last := 0, int64(0)
	replayed := map[int64]bool{}
	if scoped && resume {
		missed, resync, err := s.replay(ctx, projectID, since)
		switch {
		case err != nil:
			logger.Printf("api: stream replay from %d: %+v", since, err)
			out.event("resync", 0, struct{}{})
		case resync:
			out.event("resync", 0, struct{}{})
		default:
			for _, c := range missed {
				replayed[c.id] = true
				if c.boardID == "" || c.boardID == boardID {
					pending++
					last = max(last, c.id)
				}
			}
		}
	}

	var presenceChanged <-chan struct{}
	lastPresence := ""
	sendPresence := func() {
		if s.o.Presence == nil {
			return
		}
		presenceChanged = s.o.Presence.Changed()
		frame := presenceFrame{Sessions: []PresenceEntry{}}
		for _, e := range s.o.Presence.Entries() {
			if e.IdentityID == caller.Identity {
				frame.Sessions = append(frame.Sessions, e)
			}
		}
		b, _ := json.Marshal(frame)
		if string(b) != lastPresence {
			lastPresence = string(b)
			out.raw("presence", 0, b)
		}
	}
	sendPresence()

	ping := time.NewTicker(s.ping)
	defer ping.Stop()
	var flush <-chan time.Time
	var timer *time.Timer
	arm := func(d time.Duration) {
		if timer == nil {
			timer = time.NewTimer(d)
			flush = timer.C
		}
	}
	if pending > 0 {
		arm(0)
	}
	for !out.failed {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-live:
			if !ok {
				return
			}
			if replayed[c.id] || (c.boardID != "" && c.boardID != boardID) {
				continue
			}
			pending++
			last = max(last, c.id)
			arm(s.coalesce)
		case <-flush:
			timer, flush = nil, nil
			frame, err := s.compose(ctx, caller, projectID, boardID)
			if err != nil {
				if e, ok := domain.AsBatonDeckError(err); ok && (e.Code == domain.CodeForbidden || e.Code == domain.CodeNotFound) {
					return // the caller lost the board: a reconnect meets the pre-flight's 403
				}
				logger.Printf("api: stream board frame %s/%s: %+v", projectID, boardID, err)
				continue
			}
			out.event("board", last, frame)
			out.event("updated", 0, updatedFrame{URI: feedURI(projectID, boardID), Count: pending})
			pending = 0
		case <-ping.C:
			out.event("ping", 0, struct{}{})
		case <-presenceChanged:
			sendPresence()
		}
	}
}

// compose is a board frame as the caller: the snapshot (which is also the membership check), the
// board's first tasks and its feed.
func (s *stream) compose(ctx context.Context, caller domain.Caller, projectID, boardID string) (boardFrame, error) {
	sv := s.o.Services
	snap, err := sv.Feed.BoardSnapshot(ctx, caller, service.BoardSnapshotReq{ProjectID: projectID, BoardID: boardID})
	if err != nil {
		return boardFrame{}, err
	}
	tasks, err := sv.Tasks.List(ctx, caller, service.ListTasksReq{ProjectID: projectID, BoardID: boardID, Limit: boardTasksLimit})
	if err != nil {
		return boardFrame{}, err
	}
	feed, err := sv.Feed.BoardFeed(ctx, caller, service.BoardFeedReq{ProjectID: projectID, BoardID: boardID})
	if err != nil {
		return boardFrame{}, err
	}
	return boardFrame{Board: service.GetBoardResp{Board: snap.Board, TaskCounts: snap.TaskCounts}, Tasks: tasks, Feed: feed}, nil
}

// replay is the project's changes after since, reading at most replayCap rows of the change log
// (one ReplayAll per replayPage, so at most replayCap/replayPage+1 statements): resync when more
// rows than that follow the cursor.
func (s *stream) replay(ctx context.Context, projectID string, since int64) (out []change, resync bool, err error) {
	scanned := 0
	for {
		n := min(replayPage, s.replayCap+1-scanned)
		page, err := s.o.Bus.ReplayAll(ctx, since, n)
		if err != nil {
			return nil, false, err
		}
		for _, c := range page {
			if scanned++; scanned > s.replayCap {
				return nil, true, nil
			}
			if c.ProjectID != nil && *c.ProjectID == projectID {
				out = append(out, changeOf(c.ID, c.Payload))
			}
		}
		if len(page) < n {
			return out, false, nil
		}
		since = page[len(page)-1].ID
	}
}

// rows turns a bus subscription into the stream's changes; the channel closes with it.
func rows(ctx context.Context, sub *bus.Subscription) <-chan change {
	ch := make(chan change, streamBuffer)
	go func() {
		defer close(ch)
		for c := range sub.C() {
			select {
			case ch <- changeOf(c.ID, c.Payload):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// resumeCursor is the reconnect's cursor: Last-Event-ID (what EventSource sends on its own
// reconnect), else ?cursor=.
func resumeCursor(r *http.Request) (int64, bool, error) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("cursor")
	}
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false, domain.Validation("the cursor is not a change-log id", map[string]any{"field": "cursor"})
	}
	return n, true, nil
}

// sse writes events; after a failed write (the client went away) it writes nothing more.
type sse struct {
	w      http.ResponseWriter
	f      http.Flusher
	failed bool
}

func (s *sse) event(name string, id int64, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.failed = true
		return
	}
	s.raw(name, id, b)
}

func (s *sse) raw(name string, id int64, data []byte) {
	if s.failed {
		return
	}
	var err error
	if id > 0 {
		_, err = fmt.Fprintf(s.w, "event: %s\nid: %d\ndata: %s\n\n", name, id, data)
	} else {
		_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
	}
	if err != nil {
		s.failed = true
		return
	}
	s.f.Flush()
}
