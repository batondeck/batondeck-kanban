package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prompt is one of the four prompts (conductor src/prompts/index.ts): thin templates for the worker
// loops, each rendering one user message from its two arguments. The text is conductor's with the
// five states (ADR-0005); every tool and argument it names is in contract/expected.json.
type prompt struct {
	name, description string
	args              [2]string
	text              func(a, b string) string
}

var prompts = []prompt{
	{
		name:        "pick_up_next_task",
		description: "Claim and start the next eligible task on a board.",
		args:        [2]string{"projectId", "boardId"},
		text: func(projectID, boardID string) string {
			return fmt.Sprintf("Call next_task for project %s board %s (it returns only unblocked, unclaimed, ", projectID, boardID) +
				"eligible work). If instead you target a specific task that is blocked, walk its blockedBy chain " +
				"with get_task and start at the deepest unblocked, unclaimed blocker — work what unblocks the most, " +
				"in dependency order. claim_task it, then call get_task_context { includeUpstream: true } and USE " +
				"every populated section before you act (don't skim): the description (get_task: the context has none) + " +
				"'field' items say WHAT, 'decision'/'note' items say WHY, memory carries durable facts to reuse, " +
				"attachments carry the designs/specs (the context lists them without a link: call list_attachments " +
				"for the task, download each via its downloadUrl and actually work from it), and 'upstream' carries " +
				"the deliverables of the tasks this one depended on — build directly on those, never re-derive prior " +
				"output. If the task carries a modelHint, you MUST honor it before starting: switch to the stated " +
				"model/effort, or spawn a subagent of that size to do the work. Hold all of it for the whole task. " +
				"Record as you go so the next agent inherits a full brief: " +
				"add_context_item (decisions/notes/fields you produce), write_memory (durable facts), and keep " +
				"set_summary current — never leave the task thinner than you found it. heartbeat_task periodically; " +
				"complete_task with a concise deliverable when done (this auto-unblocks dependants). If blocked, " +
				"block_task with a reason AND blockedBy so the chain is recorded. If you must stop, handoff_task with a memoryNote."
		},
	},
	{
		name:        "triage_inbox",
		description: "Triage TODO tasks: make each a self-contained brief with priority, labels and blockers.",
		args:        [2]string{"projectId", "boardId"},
		text: func(projectID, boardID string) string {
			return fmt.Sprintf("List TODO tasks on project %s board %s (list_tasks status=TODO). A TODO task is ", projectID, boardID) +
				"claimable the moment it exists, so make each a complete, self-contained brief now — assume the worker " +
				"has NO other context, so the fields ARE the brief: a clear description + acceptance criteria; " +
				"priority/labels/requiredCapabilities (update_task); estimate each task's complexity and set modelHint " +
				"(model + effort + a one-line rationale, via update_task) so the right-sized agent picks it up; the " +
				"references it needs to start via add_context_item — 'field' (file paths, API contracts, " +
				"architecture/doc refs), 'decision' (choices already made + rationale), 'note' (gotchas), 'link' (an " +
				"http(s) URL to an external artifact — Lucid, Google Docs, Figma, …); attach the actual designs/specs " +
				"(attach_file) for any UI/spec work; record durable, reusable facts with write_memory (shared scope); " +
				"wire prerequisites with add_dependency. A title-only task is not workable: complete its brief or " +
				"delete it (delete_task); there is no parking state."
		},
	},
	{
		name:        "summarize_for_handoff",
		description: "Write a rolling summary capturing state before handing a task off.",
		args:        [2]string{"projectId", "taskId"},
		text: func(projectID, taskID string) string {
			return fmt.Sprintf("Read get_task_context for %s in %s. Write a concise status digest (what is done, ", taskID, projectID) +
				"what remains, key decisions) and store it with set_summary so the next agent can resume quickly; " +
				"set_summary takes the task's current version, which get_task returns (the context does not)."
		},
	},
	{
		name:        "decompose_into_subtasks",
		description: "Break a task into actionable subtasks.",
		args:        [2]string{"projectId", "taskId"},
		text: func(projectID, taskID string) string {
			return fmt.Sprintf("Read get_task_context for %s in %s. Break the work into a DEPENDENCY TREE, not a flat ", taskID, projectID) +
				"list: create 3–7 concrete subtasks (add_subtask) and wire add_dependency edges so each prerequisite " +
				"blocks its dependants (the server keeps reverse 'blocks' links and rejects cycles). Populate EACH " +
				"task as a complete self-contained brief (the worker gets only what's on the ticket): description + " +
				"acceptance criteria, set_summary, priority/labels/requiredCapabilities, a modelHint sized to each " +
				"subtask's complexity (model + effort + one-line rationale) so the right-sized agent picks it up, " +
				"the references it needs via " +
				"add_context_item ('field' = paths/contracts/doc refs, 'decision' = rationale, 'note' = gotchas), " +
				"attach_file for any designs/specs, and write_memory for durable facts the whole tree should reuse. " +
				"Leaf tasks (no blockers) are workable first."
		},
	},
}

// registerPrompts adds the four prompts. A missing or blank argument is invalid params, never a
// rendered prompt naming an empty project.
func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		s.AddPrompt(&mcp.Prompt{
			Name:        p.name,
			Description: p.description,
			Arguments:   []*mcp.PromptArgument{{Name: p.args[0], Required: true}, {Name: p.args[1], Required: true}},
		}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			var vals [2]string
			for i, name := range p.args {
				vals[i] = strings.TrimSpace(req.Params.Arguments[name])
				if vals[i] == "" {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("prompt %s: argument %s is required", p.name, name)}
				}
			}
			return &mcp.GetPromptResult{
				Description: p.description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(vals[0], vals[1])}}},
			}, nil
		})
	}
}
