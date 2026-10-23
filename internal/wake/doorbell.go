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
// The frame is contentless, {kind, cursor, projectId}: coalescing loses nothing, and the frame
// grants nothing; the woken agent calls the tools, which enforce every check.
package wake

import (
	"encoding/json"
	"fmt"
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

// Frame is one doorbell: its kind, the bus's safe cursor at its release, and its project.
type Frame struct {
	Kind      string `json:"kind"`
	Cursor    int64  `json:"cursor"`
	ProjectID string `json:"projectId"`
}

// Line is the frame as the receiving model reads it: one plain sentence, then the frame's JSON. It
// is a prompt (R3.1 §3.2), so it says only what to do next, never a title or a note.
func (f Frame) Line() string {
	b, _ := json.Marshal(f)
	what := map[string]string{
		KindAssigned:  "work was assigned to you",
		KindBroadcast: "open work is waiting",
		KindChanged:   "tasks changed",
	}[f.Kind]
	return fmt.Sprintf("batondeck: %s on %s (cursor %d); call claim_next, then list_notifications.\n%s", what, f.ProjectID, f.Cursor, b)
}

// row is what the dispatcher reads of a change-log row (service.changePayload).
type row struct {
	ID        int64
	Scope     string
	Kind      string
	ProjectID string
	Tool      string
	Actor     string
}

// rowOf reads a change's fields (a store.Change, which this transport does not name: ADR-0003 row 6).
func rowOf(id int64, scope string, projectID *string, kind string, payload []byte) row {
	var p struct {
		ProjectID string `json:"projectId"`
		Tool      string `json:"tool"`
		Actor     string `json:"actor"`
	}
	_ = json.Unmarshal(payload, &p)
	r := row{ID: id, Scope: scope, Kind: kind, ProjectID: p.ProjectID, Tool: p.Tool, Actor: p.Actor}
	if projectID != nil {
		r.ProjectID = *projectID
	}
	return r
}

// masterSilent is the tools whose rows ring no master: a claim (owner ruling 2026-10-23), the lease
// heartbeat, which changes no task, and the comment-like context writes ("comments ring nobody").
var masterSilent = map[string]bool{
	"claim_task": true, "claim_next": true, "heartbeat_task": true,
	"add_context_item": true, "set_summary": true, "write_memory": true, "attach_file": true,
}
