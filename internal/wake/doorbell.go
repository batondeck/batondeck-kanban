// Package wake is the device session directory and its doorbell dispatcher (R0.25, ADR-0004
// Transport 1): a map of the CLI client processes on this device (pid → entry), kept current in real
// time, and a dispatcher that writes a contentless doorbell into each client's own socket.
//
//   - Register: POST /internal/wake-session over the server's 0600 Unix socket only (register.go).
//     The pid must be alive and owned by the peer's uid; a Claude Code client's registry entry
//     (~/.claude/sessions/<pid>.json) must name the socket it registers.
//   - Unregister in real time: DELETE /internal/wake-session/{pid}, the process's exit (kqueue on
//     darwin, a pidfd on Linux: core.ExitWatcher), a failed delivery (ENOENT, ECONNREFUSED), or a
//     revoked bearer or disabled account at a delivery or the Sweep.
//   - Dispatch: one bus.Tap. Workers ring on assigned rows of their identity or agent label and on
//     broadcast rows of their projects; masters ring changed on the rows of their projects but a
//     claim's, a non-change's and their own (masterSilent). A claim or self-assignment writes no
//     assigned row (service.RingAssigned), so it rings no worker either.
//
// The frame is contentless, {kind, cursor, projectId} and the board(s) its rows name (P-191): no
// task, no title. Coalescing loses nothing, and the frame grants nothing; the woken agent calls the
// tools, which enforce every check.
package wake

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The doorbell kinds: assigned and broadcast are the change log's (service.DoorbellAssigned,
// service.DoorbellBroadcast); changed is what a master hears for any change of its projects.
const (
	KindAssigned  = "assigned"
	KindBroadcast = "broadcast"
	KindChanged   = "changed"
)

// The coalescing windows per entry and kind (plan Amendment B "The doorbell frame"): assigned is
// released AssignedQuiet after its last doorbell, within AssignedMaxHold of its first; broadcast and
// changed at most once per SlowWindow.
const (
	AssignedQuiet   = 300 * time.Millisecond
	AssignedMaxHold = time.Second
	SlowWindow      = 5 * time.Second
	// Sweep is how often every entry's bearer is re-resolved uncached and its scopes re-read: 3
	// statements per entry (GetToken or GetOAuthToken, GetUser, ListProjects), none in between.
	Sweep = 60 * time.Second
)

// MaxBoards caps a frame's boardIds; past it the frame names no board (list_boards first).
const MaxBoards = 20

// Frame is one doorbell: its kind, the bus's safe cursor at its release, its project, and the
// board its rows name (BoardID) or boards, sorted (BoardIDs). A frame naming none means "look at
// every board": a project-level row, ringAll after dropped rows, or more than MaxBoards boards.
type Frame struct {
	Kind      string   `json:"kind"`
	Cursor    int64    `json:"cursor"`
	ProjectID string   `json:"projectId"`
	BoardID   string   `json:"boardId,omitempty"`
	BoardIDs  []string `json:"boardIds,omitempty"`
}

// boards is the frame's board set.
func (f Frame) boards() []string {
	if f.BoardID != "" {
		return []string{f.BoardID}
	}
	return f.BoardIDs
}

// withBoards is f naming the distinct boards of ids, sorted: one as BoardID, several as BoardIDs,
// none (or more than MaxBoards, never a partial list) as neither.
func (f Frame) withBoards(ids []string) Frame {
	ids = slices.Clone(ids) // ids may share a frame's BoardIDs array
	slices.Sort(ids)
	ids = slices.Compact(ids)
	f.BoardID, f.BoardIDs = "", nil
	if len(ids) > MaxBoards {
		return f
	}
	if len(ids) == 1 {
		f.BoardID = ids[0]
	} else if len(ids) > 1 {
		f.BoardIDs = ids
	}
	return f
}

// merge is the frame two doorbells of one entry and kind coalesce into: the higher cursor's (a's on
// a tie), with both frames' boards when they name one project, and none when either names none (a
// "look at every board" frame is never narrowed to the boards already held). A frame of another
// project brings only its own boards, so no board is ever paired with a project it is not in.
func merge(a, b Frame) Frame {
	hi := a
	if b.Cursor > a.Cursor {
		hi = b
	}
	if a.ProjectID != b.ProjectID {
		return hi
	}
	if len(a.boards()) == 0 || len(b.boards()) == 0 {
		return hi.withBoards(nil)
	}
	return hi.withBoards(append(a.boards(), b.boards()...))
}

// Line is the frame as the receiving model reads it: one plain sentence, then the frame's JSON. It
// is a prompt (R3.1 §3.2), so it says only what to do next, never a title or a note: claim_next (a
// master's changed: next_task with its inbox, as the master command does) on the frame's board(s),
// or list_boards first when it names none.
func (f Frame) Line() string {
	b, _ := json.Marshal(f)
	what := map[string]string{
		KindAssigned:  "work was assigned to you",
		KindBroadcast: "open work is waiting",
		KindChanged:   "tasks changed",
	}[f.Kind]
	call, extra := "claim_next", ""
	if f.Kind == KindChanged {
		call, extra = "next_task", ", includeInbox: true"
	}
	var next string
	switch bs := f.boards(); len(bs) {
	case 0:
		next = fmt.Sprintf("call list_boards {projectId: %s}, then %s {projectId, boardId%s} on each board", f.ProjectID, call, extra)
	case 1:
		next = fmt.Sprintf("call %s {projectId: %s, boardId: %s%s}", call, f.ProjectID, bs[0], extra)
	default:
		next = fmt.Sprintf("call %s {projectId: %s, boardId%s} for each boardId of %s", call, f.ProjectID, extra, strings.Join(bs, ", "))
	}
	return fmt.Sprintf("batondeck: %s on %s (cursor %d); %s, then list_notifications.\n%s", what, f.ProjectID, f.Cursor, next, b)
}

// row is what the dispatcher reads of a change-log row (service.changePayload).
type row struct {
	ID        int64
	Scope     string
	Kind      string
	ProjectID string
	BoardID   string
	Tool      string
	Actor     string
}

// rowOf reads a change's fields (a store.Change, which this transport does not name: ADR-0003 row 6).
func rowOf(id int64, scope string, projectID *string, kind string, payload []byte) row {
	var p struct {
		ProjectID string `json:"projectId"`
		BoardID   string `json:"boardId"`
		Tool      string `json:"tool"`
		Actor     string `json:"actor"`
	}
	_ = json.Unmarshal(payload, &p)
	r := row{ID: id, Scope: scope, Kind: kind, ProjectID: p.ProjectID, BoardID: p.BoardID, Tool: p.Tool, Actor: p.Actor}
	if projectID != nil {
		r.ProjectID = *projectID
	}
	return r
}

// masterSilent is the tools whose rows ring no master: a claim (owner ruling 2026-10-23), the lease
// heartbeat, which changes no task, and the comment-like writes ("comments ring nobody"), add_comment
// among them since R2.1. It is a DENYLIST: every other tool's row rings the project's masters. A new
// tool that changes no task's state must be added here deliberately, with a case in
// TestDirectoryMasterSilentTools; nothing else stops it ringing.
var masterSilent = map[string]bool{
	"claim_task": true, "claim_next": true, "heartbeat_task": true,
	"add_context_item": true, "set_summary": true, "write_memory": true, "attach_file": true,
	"add_comment": true,
}
