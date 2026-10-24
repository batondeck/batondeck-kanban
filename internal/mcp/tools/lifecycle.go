package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("lifecycle", RegisterLifecycle) }

// RegisterLifecycle adds the lease lifecycle tools, each one service.Lifecycle call.
func RegisterLifecycle(s *mcp.Server, svc service.Services) {
	l := svc.Lifecycle
	mcp.Tool(s, "lifecycle", "claim_task",
		"Claim a TODO task with an exclusive lease; it moves to IN_PROGRESS. Another live lease is CONFLICT_LOCKED. A retry with the same idempotencyKey replays the original lease.",
		l.Claim)
	mcp.Tool(s, "lifecycle", "claim_next",
		"Claim the board's best claimable TODO task in one call: unleased, unblocked, past its backoff, and unassigned or assigned to you (no assignee needed). task is null when none is. An assignee naming someone else is FORBIDDEN below master. sprintId, maxConcurrency and shard.count > 1 are refused.",
		l.ClaimNext)
	mcp.Tool(s, "lifecycle", "heartbeat_task",
		"Extend your live lease to now plus leaseSeconds without bumping the task version. A lease that has expired or is not yours is LEASE_EXPIRED.",
		l.Heartbeat)
	mcp.Tool(s, "lifecycle", "release_task",
		"Release your lease; the task returns to TODO with no backoff. A retry with the same idempotencyKey replays the original result.",
		l.Release)
	mcp.Tool(s, "lifecycle", "next_task",
		"Preview the board's best claimable TODO task without claiming it, or null: unassigned or yours, or assignee's for a master. includeInbox adds its REVIEW, blocked and poisoned tasks (assignee's, when given). strategy score and explain are refused.",
		l.Next)
	mcp.Tool(s, "lifecycle", "complete_task",
		"Complete your leased task: REVIEW when the project reviews (the reviewer becomes the assignee), else DONE, which unblocks its dependents. artifacts are refused.",
		l.Complete)
	mcp.Tool(s, "lifecycle", "block_task",
		"Mark your leased task BLOCKED with a reason and drop the lease. Each blockedBy id becomes a dependency in the same transaction; a cycle writes nothing.",
		l.Block)
	mcp.Tool(s, "lifecycle", "fail_task",
		"Report your leased task failed: back to TODO after a backoff, or poisoned (BLOCKED, label poisoned) when retryable is false or the retry policy's attempt limit is reached.",
		l.Fail)
	mcp.Tool(s, "lifecycle", "reap_stale_leases",
		"Return each IN_PROGRESS task on a board whose lease has expired to TODO with a backoff, or poison it at the retry policy's attempt limit. Returns the count reaped.",
		l.Reap)
	mcp.Tool(s, "lifecycle", "handoff_task",
		"Hand your leased task to another agent: the lease is dropped, the task returns to TODO assigned to toAgent, and memoryNote is kept as the task's handoff memory.",
		l.Handoff)
	mcp.Tool(s, "lifecycle", "cancel_task",
		"Cancel a task: DONE with the label cancelled and the reason as a note, unblocking its dependents. Needs the master role or the task's live lease; version-checked.",
		l.Cancel)
}
