---
description: Sweep the batondeck-kanban board's DONE tasks into Chronicle decision records, cited back to each task.
---

Run a **Chronicle sweep** over a batondeck-kanban board. Finished tasks already carry the evidence
(the description, the deliverable, the `DONE / NEXT / REJECTED / UNCERTAIN` checkpoints, the artifacts);
the sweep lays it out as one decision record per task and cites every block back to it.

Inputs: $ARGUMENTS — optionally the project and board. Missing → `list_projects` → `list_boards`.
It runs as you, in this session, when asked; nothing schedules it.

The shape (the batondeck-chronicle skill is the full procedure — follow it):

1. `recall_memory { projectId, scope: "project", key: "chronicle.cursor" }` → the cursor, or genesis.
2. `list_tasks { projectId, boardId, status: "DONE", updatedSince: "<cursor>", limit: 50 }`, then
   every further page by `nextCursor` until none comes back: a page can be short or empty and still
   carry one, because pages are in board order and the filter applies after paging.
3. Per task, `get_task` and `get_task_context { include: ["items"] }`: both, because the context has no
   title or description.
4. Compose one page per task from the task's own fields, verbatim, every block citing the task.
5. `ingest_chronicle_page { projectId, kind: "adr", slug: "adr/<taskId>", title, blocks }`; report any
   `conflicts`.
6. `write_memory { projectId, scope: "project", key: "chronicle.cursor", value }`, the newest
   `updatedAt` of the whole window, once, after its last page is ingested; never after a partial window.

Report one line per page, the cursor you wrote or why not, any conflicts, and what remains.

Project / board: $ARGUMENTS
