package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("context", RegisterContext) }

// RegisterContext adds the per-task context tools, each one service.Context call.
func RegisterContext(s *mcp.Server, svc service.Services) {
	c := svc.Context
	mcp.Tool(s, "context", "add_context_item",
		"Add a note, decision, link or field item to a task's context; get_task_context returns it under items. A retry with the same idempotencyKey replays the original item.",
		c.AddItem)
	mcp.Tool(s, "context", "get_task_context",
		"A task's briefing: summary, fields, items, deps, attachments, memory, workflow, and resume: an earlier worker's branch, head commit and last checkpoint, null only when the task holds none.",
		c.GetTaskContext)
	mcp.Tool(s, "context", "set_summary",
		"Replace a task's rolling summary. Version-checked: a stale version is STALE. Returns the task with its new version.",
		c.SetSummary)
	mcp.Tool(s, "context", "write_memory",
		"Write a memory entry. task, agent and shared need a taskId and die with the task; project and agent_global outlive it (read them with recall_memory). Optional ttl.",
		c.WriteMemory)
	mcp.Tool(s, "context", "read_memory",
		"Read a task's memory entries, by scope (task, agent, shared) or key; agent scope is private to you. For project or agent_global memory use recall_memory.",
		c.ReadMemory)
	mcp.Tool(s, "context", "recall_memory",
		"Recall durable memory: project (any member's) and agent_global (yours, across projects). key is an exact lookup, else the newest k. projectId is needed unless scope is agent_global.",
		c.RecallMemory)
	mcp.Tool(s, "context", "add_dependency",
		"Add a dependency edge, fromTaskId blocks (or relates to) toTaskId; no status changes. An edge closing a cycle is CYCLE_DETECTED and writes nothing.",
		c.AddDependency)
	mcp.Tool(s, "context", "remove_dependency",
		"Remove a dependency edge. A dependent left with no blocker is unblocked: BLOCKED returns to TODO, unless it is poisoned.",
		c.RemoveDependency)
	mcp.Tool(s, "context", "add_subtask",
		"Create a child task under a parent, on the parent's board in its first column. Adds no dependency edge and leaves the parent as it is. Replays on idempotencyKey.",
		c.AddSubtask)
	mcp.Tool(s, "context", "list_subtasks",
		"List a parent task's child tasks.",
		c.ListSubtasks)
}
