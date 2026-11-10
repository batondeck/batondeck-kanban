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
	// StreamCoalesce is the least time between two board frames: the changes within it of the last
	// frame are gathered into one.
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
// *wake.Directory): its live entries, and a channel closed on the next register or unregister.
type Presence = service.DeviceSessions

// PresenceEntry is one live CLI client process on this device.
type PresenceEntry = service.DeviceSession

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
// the board's changes (the first at once, the rest of its StreamCoalesce at the end of it), fed by
// the bus's project subscription; presence frames from the directory.
// A board stream is authorized by a board snapshot as the caller before any byte is written (a
// non-member gets 403, an unknown board 404), and again at every board frame: a caller who has lost
// the board has the stream closed. Each board frame's SSE id is the last change-log id it covers;
// a reconnect with that cursor (Last-Event-ID, or ?cursor=) replays what it missed as one board
// frame, or, past ReplayCap rows or below the swept horizon (its own row gone), gets resync.
// Every stream, board or none, carries the caller's notifications frame (conductor's
// identity-routed notifications hub): list_notifications {limit: 50} as the caller, once on open
// and again per StreamCoalesce of the caller's notification rows, read from the bus by the
// caller's identity scope. A notification row is never a board change.
// The caller is resolved again before every ping, board, presence and notifications frame (P-219):
// once the credential no longer resolves (a logout, a disabled user as of the account cache, a
// revoked token) the stream closes and writes nothing more, so the client's reconnect meets 401.
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
	kind    string
}

func changeOf(id int64, payload []byte) change {
	var p struct {
		BoardID string `json:"boardId"`
		Kind    string `json:"kind"`
	}
	_ = json.Unmarshal(payload, &p)
	return change{id: id, boardID: p.BoardID, kind: p.Kind}
}

// notified reports whether c is a notification row: the recipient's feed, not the board.
func (c change) notified() bool { return c.kind == service.ChangeKindNotification }

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
	// The caller's own scope, on every stream: the notification rows. It also carries the caller's
	// assigned doorbells, which are left to the board subscription.
	var feed <-chan change
	if s.o.Bus != nil && s.o.Services.Notifications != nil {
		sub, err := s.o.Bus.Subscribe(ctx, []string{caller.Identity}, streamBuffer)
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		defer sub.Close()
		feed = rows(ctx, sub)
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// nginx buffers a proxied response unless told not to, which would hold every frame (and the
	// pings the SPA's stall watchdog waits for) until its buffer fills.
	h.Set("X-Accel-Buffering", "no")
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
				if !c.notified() && (c.boardID == "" || c.boardID == boardID) {
					pending++
					last = max(last, c.id)
				}
			}
		}
	}

	// recheck re-resolves the request's credential: closed when the stream must end; ok false is a
	// failed read, which keeps the stream and skips this frame (as /wake's recheck does).
	recheck := func() (closed, ok bool) {
		c, err := s.o.Auth.Reauthenticate(r)
		if err != nil {
			if e, isBD := domain.AsBatonDeckError(err); isBD && e.Code == domain.CodeUnauthenticated {
				return true, false
			}
			logger.Printf("api: stream re-resolving the caller %s: %+v", caller.Identity, err)
			return false, false
		}
		caller = c
		return false, true
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
	sendNotifications := func() {
		if s.o.Services.Notifications == nil {
			return
		}
		snap, err := s.o.Services.Notifications.List(ctx, caller, service.ListNotificationsReq{Limit: service.FeedSnapshotLimit})
		if err != nil {
			logger.Printf("api: stream notifications frame for %s: %+v", caller.Identity, err)
			return
		}
		out.event("notifications", 0, snap)
	}
	sendNotifications()
	var feedFlush <-chan time.Time

	ping := time.NewTicker(s.ping)
	defer ping.Stop()
	var flush <-chan time.Time
	var timer *time.Timer
	// Leading edge: a change a coalesce after the last board frame is sent at once; the changes
	// within one coalesce of it gather into the next frame at that coalesce's end.
	var flushed time.Time
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
			if replayed[c.id] || c.notified() || (c.boardID != "" && c.boardID != boardID) {
				continue
			}
			pending++
			last = max(last, c.id)
			arm(max(0, s.coalesce-time.Since(flushed)))
		case <-flush:
			timer, flush = nil, nil
			flushed = time.Now()
			if closed, ok := recheck(); closed {
				return
			} else if !ok {
				continue
			}
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
		case c, ok := <-feed:
			if !ok {
				return
			}
			if c.notified() && feedFlush == nil {
				feedFlush = time.After(s.coalesce)
			}
		case <-feedFlush:
			feedFlush = nil
			if closed, ok := recheck(); closed {
				return
			} else if ok {
				sendNotifications()
			}
		case <-ping.C:
			if closed, ok := recheck(); closed {
				return
			} else if ok {
				out.event("ping", 0, struct{}{})
			}
		case <-presenceChanged:
			if closed, ok := recheck(); closed {
				return
			} else if ok {
				sendPresence()
			} else {
				presenceChanged = s.o.Presence.Changed()
			}
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
// after the cursor (one ReplayAll per replayPage, the first one also reading the cursor's own row):
// resync when more rows than that follow the cursor, or when the cursor is below the change log's
// horizon (P-281). The retention sweep deletes the rows older than its cut (DeleteChangesOlderThan),
// and ts follows id order, so the cursor's own row still in the log shows that no row after it was
// swept (on Postgres ts is each serve process's own clock, so a skew between processes could break
// that order for rows written at the cut): the first read starts one below the
// cursor to see that row, which is not replayed or counted again. A cursor whose row is gone may
// have missed swept rows that ReadChanges' id > since would skip in silence, so it gets resync; if
// nothing after it was swept, that costs the client one refetch. Cursor 0 has no row of its own and
// is above the horizon while the log still starts at id 1, the first id both engines assign.
func (s *stream) replay(ctx context.Context, projectID string, since int64) (out []change, resync bool, err error) {
	anchor := max(since, 1) // the row the first page must open with
	from, scanned := anchor-1, 0
	for first := true; ; first = false {
		n := s.replayCap + 1 - scanned
		if first && since > 0 {
			n++ // the cursor's own row
		}
		n = min(replayPage, n)
		page, err := s.o.Bus.ReplayAll(ctx, from, n)
		if err != nil {
			return nil, false, err
		}
		full := len(page) == n
		if len(page) > 0 {
			from = page[len(page)-1].ID
		}
		if first {
			if len(page) == 0 || page[0].ID != anchor {
				return nil, true, nil // the horizon has passed the cursor
			}
			if since > 0 {
				page = page[1:]
			}
		}
		for _, c := range page {
			if scanned++; scanned > s.replayCap {
				return nil, true, nil
			}
			if c.ProjectID != nil && *c.ProjectID == projectID {
				out = append(out, changeOf(c.ID, c.Payload))
			}
		}
		if !full {
			return out, false, nil
		}
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
