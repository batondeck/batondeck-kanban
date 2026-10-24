package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// FollowUps are a task's directives: each addressed to the task's holder or assignee, open until
// acknowledged, and able to send REVIEW or DONE work back to TODO.
type FollowUps interface {
	// AddFollowUp is add_follow_up.
	// Tier: master. Targets the live lease holder, else the assignee; a target that is the caller is
	// dropped. reopen moves a REVIEW or DONE task to TODO, past the transition table (an automatic,
	// as auto-unblock is), and is a recorded no-op from any other status. Event: task.follow_up, plus
	// task.reopened on a reopen. Audit: none. Doorbell: assigned to the target (its identity when it
	// is a member, else its agent label), plus broadcast when a reopen leaves an unassigned TODO.
	// Replays on idempotencyKey.
	AddFollowUp(ctx context.Context, caller domain.Caller, req AddFollowUpReq) (AddFollowUpResp, error)

	// AckFollowUp is ack_follow_up.
	// Tier: write. Only the follow-up's target (its verified identity, or the live lease holder when
	// it is bound to none) or a project admin may ack a targeted follow-up; any writer an untargeted
	// one (FORBIDDEN otherwise). Event: followup.acked. Audit: none. Doorbell: none.
	AckFollowUp(ctx context.Context, caller domain.Caller, req AckFollowUpReq) (AckFollowUpResp, error)

	// ListFollowUps is list_follow_ups.
	// Tier: read. Writes nothing. Oldest first; openOnly keeps the open ones.
	ListFollowUps(ctx context.Context, caller domain.Caller, req ListFollowUpsReq) (ListFollowUpsResp, error)
}

// AddFollowUpReq is add_follow_up's input.
type AddFollowUpReq struct {
	Body           string `json:"body"`
	ProjectID      string `json:"projectId"`
	TaskID         string `json:"taskId"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
	Reopen         bool   `json:"reopen,omitzero"`
}

// AddFollowUpResp is add_follow_up's output: the follow-up and the task after it.
type AddFollowUpResp struct {
	FollowUp FollowUp `json:"followUp"`
	Task     Task     `json:"task"`
	Warnings []string `json:"warnings,omitzero"`
}

// AckFollowUpReq is ack_follow_up's input.
type AckFollowUpReq struct {
	FollowUpID string `json:"followUpId"`
	ProjectID  string `json:"projectId"`
	TaskID     string `json:"taskId"`
}

// AckFollowUpResp is ack_follow_up's output.
type AckFollowUpResp struct {
	FollowUp FollowUp `json:"followUp"`
}

// ListFollowUpsReq is list_follow_ups' input.
type ListFollowUpsReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
	Cursor    string `json:"cursor,omitzero"`
	Limit     int64  `json:"limit,omitzero"`
	OpenOnly  bool   `json:"openOnly,omitzero"`
}

// ListFollowUpsResp is list_follow_ups' output.
type ListFollowUpsResp struct {
	FollowUps  []FollowUp `json:"followUps"`
	NextCursor string     `json:"nextCursor,omitzero"`
}

// FollowUp is one follow-up: get_task_context's openFollowUps[] has the same shape. The verified
// identity behind target, which ack_follow_up authorises on, is stored but not on the wire.
type FollowUp = OpenFollowUp
