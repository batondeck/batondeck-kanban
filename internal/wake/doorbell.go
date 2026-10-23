// Package wake is the /wake doorbell (plan §Notifications path C): an SSE stream per bearer of the
// assigned and broadcast doorbells of the caller's token scopes, fed by the change-log bus. A
// reconnect with ?since=<cursor> replays every doorbell after the cursor from the log (bus.Replay)
// before going live; live doorbells are coalesced to one frame per kind per burst (Quiet). An idle stream
// costs no statement: the bus fans out in process, and only a keepalive comment is written.
//
// Wire format, one event per doorbell:
//
//	id: <cursor>
//	data: {"agent":"<assignee or empty>","kind":"assigned|broadcast","taskId":"T-1","projectId":"P-…","boardId":"B-…","cursor":<cursor>}
//
// The cursor is the change-log id of the doorbell's row (an integer, ids start at 1). Comments: ": ok"
// once the subscription is live, ": keepalive" every Keepalive, and before the server ends a stream:
// ": dropped, reconnect with since=<cursor>" (its subscription fell behind), ": scopes changed,
// reconnect with since=<cursor>" (a membership changed), ": reauthenticate" (the bearer no longer
// resolves) and ": recheck failed, reconnect with since=<cursor>". The cursor is omitted when the
// stream knows none. Every Recheck the stream re-resolves its bearer and re-reads its scopes.
//
// The payload is zero-authority: a task id and a kind, never a title or a note. A woken agent calls
// the tools, which enforce every check. broadcast is work assigned to nobody.
package wake

import (
	"encoding/json"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/bus"
)

// The doorbell kinds a frame carries (service.DoorbellAssigned, service.DoorbellBroadcast); a
// change-log row of any other kind rings nothing here.
const (
	KindAssigned  = "assigned"
	KindBroadcast = "broadcast"
)

const (
	// Keepalive is the period of the keepalive comment on a stream with nothing to say.
	Keepalive = 20 * time.Second
	// Quiet is how long a kind's doorbells must pause before their one frame is written: the bus's
	// read interval (bus.PollInterval, 250 ms) plus 50 ms for the read, so a burst the bus delivers
	// in two reads is one frame. Frames of a kind are therefore at least Quiet apart, so never more
	// than one per 250 ms (plan §Notifications "Coalescing and cost").
	Quiet = bus.PollInterval + 50*time.Millisecond
	// MaxHold bounds how long doorbells that never pause hold their frame.
	MaxHold = time.Second
	// Recheck is how often an open stream re-resolves its bearer and re-reads its scopes: a revoked
	// token, a disabled user or a changed membership ends it within Recheck (plus the resolver's own
	// account cache, auth.DefaultAccountCacheTTL). About 3 statements per stream per Recheck.
	Recheck = 60 * time.Second
)

// Frame is one doorbell on the wire.
type Frame struct {
	Agent     string `json:"agent"`
	Kind      string `json:"kind"`
	TaskID    string `json:"taskId"`
	ProjectID string `json:"projectId"`
	BoardID   string `json:"boardId"`
	Cursor    int64  `json:"cursor"`
}

// rowPayload is the part of a change-log row's payload a frame carries (service.changePayload).
type rowPayload struct {
	Agent     string `json:"agent"`
	TaskID    string `json:"taskId"`
	ProjectID string `json:"projectId"`
	BoardID   string `json:"boardId"`
}

// frameOf is the frame of a change-log row, ok false for a row that is not a doorbell.
func frameOf(id int64, kind string, payload []byte) (Frame, bool) {
	if kind != KindAssigned && kind != KindBroadcast {
		return Frame{}, false
	}
	var p rowPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Frame{}, false
	}
	return Frame{Agent: p.Agent, Kind: kind, TaskID: p.TaskID, ProjectID: p.ProjectID, BoardID: p.BoardID, Cursor: id}, true
}
