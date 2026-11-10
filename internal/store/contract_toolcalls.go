package store

import "context"

// ToolCallOutcome is a tool call's outcome.
type ToolCallOutcome string

const (
	ToolCallOK    ToolCallOutcome = "ok"
	ToolCallError ToolCallOutcome = "error"
)

// ToolCall is one tool_calls row, the fields of conductor's structured `tool` log line
// (src/tools/common.ts:655-695): Code is "OK" on success, else the error code. TS is Unix
// milliseconds. Agent and ProjectID are "" when there is none.
type ToolCall struct {
	TS        int64
	Tool      string
	Identity  string
	Agent     string
	Outcome   ToolCallOutcome
	Code      string
	LatencyMs int64
	ProjectID string
}

// ToolCalls is the tool-call analytics table: written in batches by service.ToolCalls.Record
// (ADR-0003), swept by retention, read by service.Analytics. Errors: a row with an empty tool,
// identity or code, an outcome other than ok or error, a negative latency, or a NUL character in any
// text field (Postgres cannot store one) is domain.Validation, and the whole batch is refused (the
// service drops such a row before it can ride in one); a delete limit <= 0 is domain.Validation; a
// summary of an empty project id is domain.Validation.
type ToolCalls interface {
	// InsertToolCalls writes the batch in one transaction: every row or none. An empty batch writes
	// nothing and is no error.
	InsertToolCalls(ctx context.Context, batch []ToolCall) error
	// DeleteToolCallsOlderThan deletes at most limit rows whose TS is before before, oldest first,
	// and returns how many it deleted.
	DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error)
	// SummarizeToolCalls is projectID's rows with TS >= since, one group per (tool, outcome, agent,
	// identity, UTC day), ordered by those five: one statement.
	SummarizeToolCalls(ctx context.Context, projectID string, since int64) ([]ToolCallGroup, error)
}

// ToolCallGroup is one group of a summary: Day is the UTC date "YYYY-MM-DD", LatencySum the sum of
// its rows' LatencyMs.
type ToolCallGroup struct {
	Tool       string
	Outcome    ToolCallOutcome
	Agent      string
	Identity   string
	Day        string
	Calls      int64
	LatencySum int64
}

var (
	_ ToolCalls = (*SQLite)(nil)
	_ ToolCalls = (*Postgres)(nil)
)
