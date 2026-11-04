package conformance

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("context", contextItems) }

// contextItems is the context-item area: append, page newest or oldest first, the per-task delete,
// project isolation, and the refusals.
func contextItems(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListContextItems(ctx, "P-probe", "T-1", store.ContextItemsQuery{})
		return err
	}
	area(t, "ContextItems", newStore, probe, func(t *testing.T) {
		add := func(t *testing.T, s store.Store, projectID, taskID, body string) store.ContextItem {
			t.Helper()
			it, err := s.AddContextItem(ctx, store.AddContextItemInput{ProjectID: projectID, TaskID: taskID, Kind: store.ContextNote, Body: body, Author: "alice"})
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return it
		}
		bodies := func(items []store.ContextItem) []string {
			out := make([]string, len(items))
			for i, it := range items {
				out[i] = it.Body
			}
			return out
		}

		t.Run("AddReturnsTheStampedItem", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			it, err := s.AddContextItem(ctx, store.AddContextItemInput{ProjectID: p, TaskID: task, Kind: store.ContextDecision, Body: "use sqlite", Author: "alice"})
			noErr(t, err)
			if it.ID == "" || it.TS == 0 || it.ProjectID != p || it.TaskID != task || it.Kind != store.ContextDecision || it.Body != "use sqlite" || it.Author != "alice" {
				t.Fatalf("AddContextItem = %+v", it)
			}
		})

		t.Run("ListIsNewestFirstAndPagesByCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, body := range []string{"one", "two", "three"} {
				add(t, s, p, task, body)
			}
			page, err := s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Limit: 2})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 2 || got[0] != "three" || got[1] != "two" || page.NextCursor == "" {
				t.Fatalf("first page %v, cursor %q: want [three two] and a cursor", got, page.NextCursor)
			}
			page, err = s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Limit: 2, Cursor: page.NextCursor})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 1 || got[0] != "one" || page.NextCursor != "" {
				t.Fatalf("second page %v, cursor %q: want [one] and no cursor", got, page.NextCursor)
			}
		})

		// P-117's resume reads the newest checkpoint note off this order. Items added back to back,
		// with no sleep, share milliseconds; the random id must not decide which is newest.
		t.Run("AddsInOneMillisecondKeepInsertionOrder", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			const n = 40
			var prev int64
			for i := range n {
				it, err := s.AddContextItem(ctx, store.AddContextItemInput{ProjectID: p, TaskID: task, Kind: store.ContextNote, Body: strconv.Itoa(i), Author: "alice"})
				noErr(t, err)
				if it.TS <= prev {
					t.Fatalf("item %d stamped %d, not after the previous item's %d", i, it.TS, prev)
				}
				prev = it.TS
			}
			page, err := s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Limit: n})
			noErr(t, err)
			got := bodies(page.Items)
			for i, body := range got {
				if body != strconv.Itoa(n-1-i) {
					t.Fatalf("newest first %v: want the reverse of the insertion order", got)
				}
			}
			if len(got) != n {
				t.Fatalf("listed %d items, want %d", len(got), n)
			}
		})

		t.Run("AscendingIsOldestFirstAndPagesByCursor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, body := range []string{"one", "two", "three"} {
				add(t, s, p, task, body)
			}
			page, err := s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Limit: 2, Ascending: true})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 2 || got[0] != "one" || got[1] != "two" {
				t.Fatalf("first ascending page %v, want [one two]", got)
			}
			page, err = s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Limit: 2, Ascending: true, Cursor: page.NextCursor})
			noErr(t, err)
			if got := bodies(page.Items); len(got) != 1 || got[0] != "three" || page.NextCursor != "" {
				t.Fatalf("second ascending page %v, cursor %q: want [three] and no cursor", got, page.NextCursor)
			}
		})

		t.Run("ATaskIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "p")[0]
			qt := seedTasks(t, sd, q, qb, "q")[0]
			if pt != qt {
				t.Fatalf("the two projects' first tasks are %s and %s: ids are per project, both should be T-1", pt, qt)
			}
			add(t, s, p, pt, "p's note")
			page, err := s.ListContextItems(ctx, q, qt, store.ContextItemsQuery{})
			noErr(t, err)
			if len(page.Items) != 0 {
				t.Fatalf("%s/%s lists %v: another project's item leaked", q, qt, bodies(page.Items))
			}
		})

		t.Run("DeleteByTaskDeletesThatTasksItemsOnly", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			tasks := seedTasks(t, sd, p, b, "a", "b")
			add(t, s, p, tasks[0], "a1")
			add(t, s, p, tasks[0], "a2")
			add(t, s, p, tasks[1], "b1")
			n, err := s.DeleteContextItemsByTask(ctx, p, tasks[0])
			noErr(t, err)
			if n != 2 {
				t.Fatalf("deleted %d, want 2", n)
			}
			left, err := s.ListContextItems(ctx, p, tasks[1], store.ContextItemsQuery{})
			noErr(t, err)
			if got := bodies(left.Items); len(got) != 1 || got[0] != "b1" {
				t.Fatalf("the other task's items %v, want [b1]", got)
			}
			if n, err := s.DeleteContextItemsByTask(ctx, p, tasks[0]); err != nil || n != 0 {
				t.Fatalf("a second delete: %d, %v; want 0 and no error", n, err)
			}
		})

		t.Run("AddAndListRefuseAMissingTask", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.AddContextItem(ctx, store.AddContextItemInput{ProjectID: p, TaskID: "T-404", Kind: store.ContextNote, Body: "x", Author: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.ListContextItems(ctx, p, "T-404", store.ContextItemsQuery{})
			wantErr(t, err, domain.CodeNotFound, "Task")
		})

		t.Run("AddRefusesABadKindBodyOrAuthor", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, in := range []store.AddContextItemInput{
				{ProjectID: p, TaskID: task, Kind: "memo", Body: "x", Author: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.ContextNote, Body: "", Author: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.ContextNote, Body: "x", Author: ""},
			} {
				_, err := s.AddContextItem(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			page, err := s.ListContextItems(ctx, p, task, store.ContextItemsQuery{})
			noErr(t, err)
			if len(page.Items) != 0 {
				t.Fatalf("refused adds left %d items", len(page.Items))
			}
		})

		t.Run("AMalformedCursorIsValidation", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.ListContextItems(ctx, p, task, store.ContextItemsQuery{Cursor: "not a cursor!"})
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
