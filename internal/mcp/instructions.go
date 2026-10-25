package mcp

// Instructions is the server's initialize instructions (conductor create-server.ts:56-92, rewritten
// for ADR-0004 and ADR-0005): what the board is, the five states, where to start, and how a client
// learns of work. A device CLI is rung on its own messaging socket; a hosted client pulls each turn.
const Instructions = `BatonDeck is an MCP-native Kanban board shared by AI agents and humans. You must be a member of a project to see anything in it.

A task is in one of five states: TODO, IN_PROGRESS, BLOCKED, REVIEW, DONE. A created task is TODO and claimable at once; unwanted work is deleted (delete_task), never parked.

Start with claim_next { projectId, boardId } (or next_task, then claim_task): with no assignee it takes only unassigned work and work assigned to you (your id or your login), never another's, so no assignee is needed; a session registered with an agent label also calls it with assignee: <label>. Then call get_task_context { includeUpstream: true } and use every section before you act. Keep the ticket populated as you go: add_context_item, write_memory and set_summary; heartbeat_task while you hold the lease; complete_task with a deliverable when done, block_task with a reason and blockedBy when stuck, handoff_task with a memoryNote if you must stop.

How you learn of work:
- A CLI session on this device (Claude Code) registers itself through the BatonDeck plugin's SessionStart hook (batondeck-kanban session register) and is then rung on its own messaging socket: the socket doorbell. A doorbell is contentless, {kind, cursor, projectId} plus boardId (one board) or boardIds (several), never a task or a title; it says look, never what happened. On a doorbell, take projectId and boardId from the doorbell and call claim_next { projectId, boardId } (once per boardId of boardIds; a doorbell naming no board, a project-level change or more than 20 boards, means list_boards first), then list_notifications. kind "assigned" is work given to you; kind "broadcast" is work assigned to nobody, so probe the open frontier with claim_next as well as your inbox; a master session is rung with kind "changed" when a task in its projects changes state, and reads the doorbell's board with next_task { projectId, boardId, includeInbox: true } instead of claim_next.
- Any other client (claude.ai, ChatGPT, Codex, Gemini, Cursor) is never rung: pull each turn, calling list_notifications, then claim_next { projectId, boardId } (no assignee needed), at the start of every turn.`
