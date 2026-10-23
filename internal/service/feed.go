package service

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// feed is service.Feed over store.Events.ListBoardFeed and the board snapshot cache. It writes
// nothing. Both methods are read tier on the board's project, under get_board's posture: neither is
// a tool of its own.
type feed struct{ c *Core }

var _ Feed = feed{}

// Feed is the board feed and board snapshot service.
func (c *Core) Feed() Feed { return feed{c} }

// feedTool is the posture the feed's reads are authorized under.
const feedTool = "get_board"

func (f feed) BoardFeed(ctx context.Context, caller domain.Caller, req BoardFeedReq) (BoardFeedResp, error) {
	if _, err := f.c.authorize(ctx, caller, req.ProjectID, feedTool, domain.GrantSubject{BoardID: req.BoardID}, false); err != nil {
		return BoardFeedResp{}, err
	}
	if req.BoardID == "" {
		return BoardFeedResp{}, domain.Validation("boardId is required", map[string]any{"field": "boardId"})
	}
	// DEVIATION: the page is in the store's order, newest first, and the cursor pages older. The
	// contract says oldest first after the cursor, which ListBoardFeed cannot serve in bounded reads.
	page, err := f.c.store.ListBoardFeed(ctx, req.ProjectID, req.BoardID, req.Cursor, int(req.Limit))
	if err != nil {
		return BoardFeedResp{}, f.c.mapErr("board_feed", err)
	}
	out := BoardFeedResp{Events: make([]BoardEvent, 0, len(page.Events)), NextCursor: page.NextCursor}
	for _, e := range page.Events {
		be, err := boardEventOut(e)
		if err != nil {
			return BoardFeedResp{}, f.c.mapErr("board_feed", err)
		}
		out.Events = append(out.Events, be)
	}
	return out, nil
}

func boardEventOut(e store.Event) (BoardEvent, error) {
	out := BoardEvent{ID: e.ID, ProjectID: e.ProjectID, Type: e.Type, Actor: e.Actor, Ts: isoMs(e.Ts), Data: map[string]any{}}
	if e.BoardID != nil {
		out.BoardID = *e.BoardID
	}
	if e.TaskID != nil {
		out.TaskID = *e.TaskID
	}
	if e.Agent != nil {
		out.Agent = *e.Agent
	}
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &out.Data); err != nil {
			return BoardEvent{}, err
		}
	}
	return out, nil
}

func (f feed) BoardSnapshot(ctx context.Context, caller domain.Caller, req BoardSnapshotReq) (BoardSnapshotResp, error) {
	if _, err := f.c.authorize(ctx, caller, req.ProjectID, feedTool, domain.GrantSubject{BoardID: req.BoardID}, false); err != nil {
		return BoardSnapshotResp{}, err
	}
	return f.c.boardSnapshot(ctx, req.ProjectID, req.BoardID)
}

// boardSnapshot is the board and its counts, from the cache when no change for the board has been
// seen since it was composed, else one store.GetBoard (two statements). Until the bus has delivered
// a change (ObserveChange), nothing is cached: no row would ever drop the entry.
func (c *Core) boardSnapshot(ctx context.Context, projectID, boardID string) (BoardSnapshotResp, error) {
	if boardID == "" {
		return BoardSnapshotResp{}, domain.Validation("boardId is required", map[string]any{"field": "boardId"})
	}
	key := [2]string{projectID, boardID}
	snap, gen, cursor, ok := c.boards.get(key)
	if ok {
		return snap, nil
	}
	b, counts, err := c.store.GetBoard(ctx, projectID, boardID)
	if err != nil {
		return BoardSnapshotResp{}, c.mapErr("get_board", err)
	}
	snap = BoardSnapshotResp{Board: boardOut(b), TaskCounts: make(map[string]float64, len(counts)), Cursor: cursor}
	for id, n := range counts {
		snap.TaskCounts[id] = float64(n)
	}
	c.boards.put(key, snap, gen)
	return cloneSnapshot(snap), nil
}

// ObserveChange is the snapshot cache's invalidation hook: the bus calls it with every change-log
// row it delivers, and the cache serves nothing before its first call. A row naming a board drops
// that board's snapshot; a row naming only a project drops every snapshot of the project. The
// highest id seen is the cursor a snapshot is composed at.
func (c *Core) ObserveChange(ch store.Change) {
	var p struct {
		ProjectID string `json:"projectId"`
		BoardID   string `json:"boardId"`
	}
	_ = json.Unmarshal(ch.Payload, &p)
	if p.ProjectID == "" && ch.ProjectID != nil {
		p.ProjectID = *ch.ProjectID
	}
	c.boards.observe(ch.ID, p.ProjectID, p.BoardID)
}

// dropSnapshots drops a board's snapshot (every board of the project when boardID is "") after a
// mutation of this process commits, so a writer reads its own write before the bus delivers the row.
func (c *Core) dropSnapshots(projectID, boardID string) {
	c.boards.drop(projectID, boardID)
}

// snapshotCache holds one BoardSnapshotResp per (project, board) once live (the bus has delivered a
// change). gen counts drops: a composition that began before one does not store what it read, so a
// drop is never undone by a read in flight.
// ponytail: one entry per board read, never evicted; bound it if boards per process grow large.
type snapshotCache struct {
	mu      sync.Mutex
	live    bool
	gen     uint64
	cursor  int64
	entries map[[2]string]BoardSnapshotResp
}

func newSnapshotCache() *snapshotCache {
	return &snapshotCache{entries: map[[2]string]BoardSnapshotResp{}}
}

func (s *snapshotCache) get(key [2]string) (BoardSnapshotResp, uint64, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return BoardSnapshotResp{}, s.gen, s.cursor, false
	}
	return cloneSnapshot(e), s.gen, s.cursor, true
}

func (s *snapshotCache) put(key [2]string, snap BoardSnapshotResp, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.live || gen != s.gen {
		return
	}
	s.entries[key] = cloneSnapshot(snap)
}

func (s *snapshotCache) observe(id int64, projectID, boardID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live = true
	if id > s.cursor {
		s.cursor = id
	}
	if projectID != "" {
		s.dropLocked(projectID, boardID)
	}
}

func (s *snapshotCache) drop(projectID, boardID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(projectID, boardID)
}

func (s *snapshotCache) dropLocked(projectID, boardID string) {
	s.gen++
	if boardID != "" {
		delete(s.entries, [2]string{projectID, boardID})
		return
	}
	for k := range s.entries {
		if k[0] == projectID {
			delete(s.entries, k)
		}
	}
}

// cloneSnapshot copies what a caller could change in place, so a cached entry is never mutated.
func cloneSnapshot(s BoardSnapshotResp) BoardSnapshotResp {
	s.Board.Columns = slices.Clone(s.Board.Columns)
	s.Board.Transitions = maps.Clone(s.Board.Transitions)
	s.TaskCounts = maps.Clone(s.TaskCounts)
	return s
}
