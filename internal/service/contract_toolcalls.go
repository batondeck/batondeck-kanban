package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// ToolCalls records one row per MCP tool call (conductor's `tool` log line): the transport calls
// Record once per call, after the handler, success or error. It writes no event, audit row or
// doorbell, and it never fails the call it records.
type ToolCalls interface {
	// Record writes one tool_calls row; identity and agent come from the caller.
	Record(ctx context.Context, caller domain.Caller, req RecordToolCallReq) (RecordToolCallResp, error)
}

// RecordToolCallReq is one finished call.
type RecordToolCallReq struct {
	Tool      string `json:"tool"`
	Outcome   string `json:"outcome"` // "ok" or "error"
	Code      string `json:"code"`    // "OK", else the domain.Code returned
	LatencyMs int64  `json:"latencyMs"`
	ProjectID string `json:"projectId,omitzero"`
}

// RecordToolCallResp is empty.
type RecordToolCallResp struct{}
