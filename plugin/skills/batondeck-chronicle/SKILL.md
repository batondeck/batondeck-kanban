---
name: batondeck-chronicle
description: "Sweep a batondeck-kanban board's DONE tasks into Chronicle decision records: read the cursor from project memory, list the tasks that reached DONE since it (every page, following nextCursor to the end), pull each task's description, deliverable, checkpoints and artifacts, lay them out as cited blocks and ingest each page with ingest_chronicle_page, then advance the cursor once, after the whole window is ingested. Invoked by /batondeck-kanban:chronicle."
---

# batondeck-kanban chronicle

You turn finished tasks into **decision records**: why a thing was built, how it was built, and what
review changed. The evidence is already on the board: descriptions, deliverables, the
`DONE / NEXT / REJECTED / UNCERTAIN` checkpoints, artifacts. The sweep lays it out and cites every block
back to the task it came from.

**You extract and lay out; you never invent a sentence.** Every block's text is copied from a task
field. When the evidence is missing, the record says so in a coverage block ("Not found on the task:
rejected alternatives"), and only after you read the field and found it empty: claiming an absence
you did not verify is the one failure this procedure cannot survive.

## 1. Read the cursor

```
recall_memory { projectId, scope: "project", key: "chronicle.cursor" }
```

The cursor is `entries[0].value`, an ISO instant: the newest `updatedAt` a previous sweep processed.
No entry means genesis. It lives in project memory, not a file, so two checkouts cannot fork it.

## 2. Take the window

```
list_tasks { projectId, boardId, status: "DONE", updatedSince: "<cursor>", limit: 50 }
```

Genesis: the same call without `updatedSince`. **The window is every page, not the first one.**
`list_tasks` pages in board order, not by time, and applies `updatedSince` to each page after paging,
so a page can come back short or even empty while `nextCursor` is still set, and the most recently
finished task can sit on any page. Keep calling with `cursor: "<nextCursor>"` until a response carries
no `nextCursor`, taking steps 3 to 5 for the tasks of each page as it comes, and keep the newest
`updatedAt` among every task you listed. The filter is inclusive at the boundary, so re-reading your
own instant costs one duplicate task, which ingests as unchanged. Only when the whole window listed
no task at all: stop and say so, having written nothing.

## 3. Pull each task's evidence: two calls, and it has to be two

```
get_task         { projectId, taskId }                       → title, description, deliverable, artifacts, labels
get_task_context { projectId, taskId, include: ["items"] }   → items[] (each with a kind and a body)
```

`get_task_context` returns **no title and no description**, and its context items come back under
**`items`**. A checkpoint is **one** item holding all four headings: its first line is `DONE` and
`REJECTED` sits further down the same body, so read the whole body, never only its first line. A task
with neither a description nor a deliverable carries no evidence: skip it, do not write prose for it.

## 4. Compose the page

One page per task: `kind: "adr"`, `slug: "adr/<taskId>"` (the task id, never the title, so a retitle
does not fork the page), `title` the task's title. Blocks, each `{ id, text, citations }`:

| id | text, copied from |
|---|---|
| `ctx1` | the description: the context and the problem |
| `dec1` | the deliverable: what was built |
| `rej1`, `rej2`, … | each REJECTED entry of the checkpoints, with its reason |
| `unc1`, … | each UNCERTAIN entry |
| `ev1`, … | one per artifact: its `kind` and its `url` or `ref` |
| `cov1` | coverage: which of the above the task had, and which it did not |

Every block cites its task: `citations: [{ projectId, boardId, taskId }]`, the full triple (task ids are
unique only within a project). Keep the ids stable per section: a re-ingest merges by block id, so a
block that changes id reads as a new block.

## 5. Ingest

```
ingest_chronicle_page { projectId, kind: "adr", slug, title, blocks }
```

The first ingest creates the page. A re-ingest **merges**: unchanged blocks take the new text, blocks
a person edited are kept, and blocks changed on both sides come back in `conflicts` for a person to
resolve. Report conflicts; do not resolve them. `unchanged: true` means nothing moved.

## 6. Advance the cursor last

```
write_memory { projectId, scope: "project", key: "chronicle.cursor", value: "<newest updatedAt of the whole window>" }
```

Once, after the **last page** of the window is listed and every task on every page ingested. Never
after one page, and never when the sweep stops early: the pages are in board order, so a cursor
written mid-window passes over every task on the pages not yet read, for good. A sweep that stops or
dies before the end leaves the cursor where the last complete sweep put it, and the next run lists
the whole window again; the slug is a function of the task, so the failure mode is a repeated ingest
(unchanged), never a skipped task.

## Report

One line per page (task id, slug, block count, which sections it had), the cursor you wrote or why you
did not, any conflicts, and, if you stopped before the last page, that the cursor did not move.
