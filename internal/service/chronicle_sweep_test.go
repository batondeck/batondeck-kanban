package service

import (
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The Chronicle sweep's cursor (plugin/skills/batondeck-chronicle/SKILL.md, steps 1, 2 and 6) over
// list_tasks as it pages: in board order, updatedSince applied after paging, and a task that
// reaches DONE keeps its order key (complete_task writes none). A task that finishes while a sweep
// runs is listed by that sweep or by the next one, never by neither, even when it finishes behind
// the read position and another finishes ahead of it.
func TestChronicleSweepCursorPassesOverNoTaskThatFinishesMidSweep(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx, tasks := e.c.Context(), e.c.Tasks()
		seed := func(order string) store.Task {
			t.Helper()
			task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
				ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.InProgress), Title: "t" + order,
				Status: domain.InProgress, OrderKey: order, CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return task
		}
		// complete_task's write: DONE at the server's clock, the order key as it was.
		finish := func(task store.Task) {
			t.Helper()
			time.Sleep(3 * time.Millisecond) // each write its own millisecond
			cur := e.get(t, task.ID)
			if _, err := e.st.SetStatus(e.ctx, e.project, cur.ID, cur.Version, store.StatusChange{
				Status: domain.Done, ColumnID: e.column(t, domain.Done), ClearClaim: true, UpdatedBy: "alice", UpdatedAt: time.Now().UnixMilli(),
			}); err != nil {
				t.Fatal(err)
			}
		}
		// Board order: c, a, b, x. a and b are finished before the sweep starts.
		c, a, b, x := seed("a1"), seed("a2"), seed("a3"), seed("a4")
		finish(a)
		finish(b)
		time.Sleep(3 * time.Millisecond)

		// Step 1: the sweep's start, in the server's clock, from the ts of an entry it writes.
		key := "chronicle.start." + e.board.ID
		w, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, Scope: domain.MemoryScopeProject, Key: key, Value: "genesis"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{ProjectID: e.project, Scope: "project", Key: key, K: 5})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(r.Entries, func(m MemoryEntry) bool { return m.ID == w.EntryID })
		if i < 0 {
			t.Fatalf("recall_memory %+v does not hold the start entry %s", r.Entries, w.EntryID)
		}
		start, err := time.Parse(time.RFC3339Nano, r.Entries[i].Ts)
		if err != nil {
			t.Fatal(err)
		}

		// Step 2: the window a page at a time (limit 1: each DONE task its own page), with c
		// finishing after the first page was read, behind it, and x after c, ahead of it.
		page := func(since, cursor string) ListTasksResp {
			t.Helper()
			p, err := tasks.List(e.ctx, e.alice, ListTasksReq{ProjectID: e.project, BoardID: e.board.ID, Status: domain.Done, UpdatedSince: since, Cursor: cursor, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		window := func(since string, between func()) (ids []string, newest string) {
			t.Helper()
			for p, n := page(since, ""), 0; ; p, n = page(since, p.NextCursor), n+1 {
				for _, task := range p.Tasks {
					ids = append(ids, task.ID)
					newest = max(newest, task.UpdatedAt)
				}
				if n == 0 && between != nil {
					between()
				}
				if p.NextCursor == "" {
					return ids, newest
				}
			}
		}
		listed, newest := window("", func() { finish(c); finish(x) })
		if !slices.Equal(listed, []string{a.ID, b.ID, x.ID}) {
			t.Fatalf("the sweep listed %v, want [%s %s %s]: c finished behind the read position, x ahead of it", listed, a.ID, b.ID, x.ID)
		}

		// Step 6: the cursor is the start less a minute (a write in flight at the start carries a
		// time before it). The next window holds c.
		cursor := start.Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		if next, _ := window(cursor, nil); !slices.Contains(next, c.ID) {
			t.Fatalf("the next window from the cursor %s = %v, want %s in it: a task that finished mid-sweep was passed over for good", cursor, next, c.ID)
		}
		// Why not the newest updatedAt listed: that is x's, later than c's, and the next window from
		// it never lists c. Should list_tasks come to page by updatedAt, this no longer holds and the
		// skill's rule can be simplified.
		if next, _ := window(newest, nil); slices.Contains(next, c.ID) {
			t.Fatalf("the next window from the newest updatedAt listed (%s) = %v holds %s: list_tasks no longer pages in board order; revisit the chronicle skill's cursor rule", newest, next, c.ID)
		}
	})
}
