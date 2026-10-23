package cli

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// cascadeTables is every table that keys a row on a task, by the column naming it: what a deleted
// run must leave nothing in (plan §Fold 2's cascade, R1.3 AC6's list read as SQL).
var cascadeTables = [][2]string{
	{"tasks", "id"}, {"task_events", "task_id"}, {"artifacts", "task_id"}, {"summaries", "task_id"},
	{"runs", "run_task_id"}, {"context_items", "task_id"}, {"memory", "task_id"}, {"comments", "task_id"},
	{"follow_ups", "task_id"}, {"attachments", "task_id"}, {"dependencies", "blocker_id"}, {"dependencies", "dependent_id"},
}

// counter is one engine's row count on its own connection: query with the engine's placeholders.
type counter struct {
	t     *testing.T
	ph    func(i int) string
	count func(q string, args ...any) int
}

// rows is, per table and column, how many rows of project p name one of ids.
func (c counter) rows(p string, ids ...string) map[string]int {
	c.t.Helper()
	out := map[string]int{}
	for _, tc := range cascadeTables {
		marks := make([]string, len(ids))
		args := []any{p}
		for i, id := range ids {
			marks[i] = c.ph(i + 2)
			args = append(args, id)
		}
		q := fmt.Sprintf("SELECT count(*) FROM %s WHERE project_id = %s AND %s IN (%s)", tc[0], c.ph(1), tc[1], strings.Join(marks, ", "))
		out[tc[0]+"."+tc[1]] = c.count(q, args...)
	}
	return out
}

// onRawEngines runs fn on each selected SQL engine over a joined composition whose rows fn can count
// on a connection of its own.
func onRawEngines(t *testing.T, fn func(t *testing.T, j *joined, c counter)) {
	for _, e := range servicetest.Engines(t) {
		if e == servicetest.Memstore {
			continue
		}
		t.Run(e, func(t *testing.T) {
			var st store.Store
			c := counter{t: t}
			switch e {
			case servicetest.SQLite:
				s, path := storetest.SQLiteFile(t)
				db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				st, c.ph = s, func(int) string { return "?" }
				c.count = func(q string, args ...any) int {
					var n int
					if err := db.QueryRow(q, args...).Scan(&n); err != nil {
						t.Fatalf("%s: %v", q, err)
					}
					return n
				}
			case servicetest.Postgres:
				s, dsn := storetest.PostgresDSN(t)
				conn, err := pgx.Connect(context.Background(), dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close(context.Background()) })
				st, c.ph = s, func(i int) string { return fmt.Sprintf("$%d", i) }
				c.count = func(q string, args ...any) int {
					var n int
					if err := conn.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
						t.Fatalf("%s: %v", q, err)
					}
					return n
				}
			}
			fn(t, joinStore(t, e, st), c)
		})
	}
}

// AC1 on each SQL engine, through the served binary's /mcp with real API tokens: pick_run over three
// runs leaves the two losers no row in any table that keys on a task, the winner and its run row
// remain, and the parent carries two run.discarded events, racing false. AC2: cancel_runs leaves no
// row of either run and list_runs is empty.
func TestRaceLosersLeaveNoRow(t *testing.T) {
	onRawEngines(t, func(t *testing.T, j *joined, c counter) {
		owner, will := j.user("ada"), j.user("will")
		b := j.board(owner, nil, map[*agent]string{will: "worker"})
		parent := asStr(b.task("parent")["id"])
		started := owner.call("start_runs", map[string]any{"projectId": b.project, "taskId": parent, "version": b.get(parent)["version"], "count": 3})
		var runs []string
		for _, r := range started["runs"].([]any) {
			runs = append(runs, asStr(asMap(r)["id"]))
		}
		win, losers := runs[0], runs[1:]
		lease := asStr(owner.call("claim_task", map[string]any{"projectId": b.project, "taskId": win})["leaseId"])
		owner.call("complete_task", map[string]any{"projectId": b.project, "taskId": win, "leaseId": lease, "deliverable": "won"})
		after := asStr(b.task("downstream")["id"])
		owner.call("add_context_item", map[string]any{"projectId": b.project, "taskId": losers[0], "kind": "note", "body": "n"})
		owner.call("write_memory", map[string]any{"projectId": b.project, "taskId": losers[0], "scope": "task", "key": "k", "value": "v"})
		owner.call("set_summary", map[string]any{"projectId": b.project, "taskId": losers[0], "version": b.get(losers[0])["version"], "summary": "s"})
		// The composition has no blob keyring here, so the attachment row is the store's.
		if _, err := j.st.CreateAttachment(context.Background(), store.CreateAttachmentInput{ProjectID: b.project, TaskID: losers[0], MimeType: "text/plain", Bytes: 5, Kind: store.AttachmentFile, CreatedBy: owner.id}); err != nil {
			t.Fatal(err)
		}
		owner.call("add_dependency", map[string]any{"projectId": b.project, "fromTaskId": losers[0], "toTaskId": after})
		will.call("claim_task", map[string]any{"projectId": b.project, "taskId": losers[1]})
		before := c.rows(b.project, losers...)
		for _, k := range []string{"tasks.id", "runs.run_task_id", "context_items.task_id", "summaries.task_id", "attachments.task_id", "dependencies.blocker_id", "task_events.task_id"} {
			if before[k] == 0 {
				t.Fatalf("before the pick %s holds no row of the losers: the probe would pass vacuously (%v)", k, before)
			}
		}

		picked := asMap(owner.call("pick_run", map[string]any{"projectId": b.project, "taskId": parent, "runTaskId": win, "version": b.get(parent)["version"]})["task"])
		if picked["racing"] == true || picked["status"] != "REVIEW" {
			t.Fatalf("pick_run: %v", picked)
		}
		left := c.rows(b.project, losers...)
		total := 0
		for _, n := range left {
			total += n
		}
		if total != 0 {
			t.Fatalf("the losers %v still hold rows: %v", losers, left)
		}
		if w := c.rows(b.project, win); w["tasks.id"] != 1 || w["runs.run_task_id"] != 1 {
			t.Fatalf("the winner %s: %v, want its task and run rows", win, w)
		}
		discarded := c.count(fmt.Sprintf("SELECT count(*) FROM task_events WHERE project_id = %s AND task_id = %s AND type = 'run.discarded'", c.ph(1), c.ph(2)), b.project, parent)
		if discarded != 2 {
			t.Fatalf("the parent carries %d run.discarded, want 2", discarded)
		}
		t.Logf("%s: before the pick %v; after it the losers %v hold %d rows across %d table columns; the parent %s has %d run.discarded",
			j.engine, nonZero(before), losers, total, len(cascadeTables), parent, discarded)

		again := asStr(b.task("again")["id"])
		started = owner.call("start_runs", map[string]any{"projectId": b.project, "taskId": again, "version": b.get(again)["version"], "count": 2})
		runs = nil
		for _, r := range started["runs"].([]any) {
			runs = append(runs, asStr(asMap(r)["id"]))
		}
		back := asMap(owner.call("cancel_runs", map[string]any{"projectId": b.project, "taskId": again, "version": b.get(again)["version"]})["task"])
		listed := owner.call("list_runs", map[string]any{"projectId": b.project, "taskId": again})["runs"].([]any)
		gone := c.rows(b.project, runs...)
		sum := 0
		for _, n := range gone {
			sum += n
		}
		if back["status"] != "TODO" || back["racing"] == true || len(listed) != 0 || sum != 0 {
			t.Fatalf("cancel_runs: %v; list_runs %v; rows %v", back, listed, gone)
		}
		t.Logf("%s: cancel_runs → %s TODO, list_runs [], runs %v hold %d rows", j.engine, again, runs, sum)
	})
}

func nonZero(m map[string]int) string {
	var out []string
	for k, n := range m {
		if n > 0 {
			out = append(out, fmt.Sprintf("%s=%d", k, n))
		}
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}
