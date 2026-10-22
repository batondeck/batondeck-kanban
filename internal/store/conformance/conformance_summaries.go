package conformance

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("summaries", summaries) }

// summaries is the summary area: empty until set, set and replace under the task's version, STALE
// with expected and actual and nothing written, the version left for the caller to bump, and a
// missing task.
func summaries(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.GetSummary(ctx, "P-probe", "T-1")
		return err
	}
	area(t, "Summaries", newStore, probe, func(t *testing.T) {
		t.Run("ATaskWithoutOneReadsEmpty", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			got, err := s.GetSummary(ctx, p, task)
			noErr(t, err)
			if got.Body != "" || got.UpdatedAt != 0 {
				t.Fatalf("GetSummary = %+v, want an empty summary", got)
			}
		})

		t.Run("SetReplacesUnderTheTasksVersionAndLeavesTheVersion", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			v := stateOf(t, sd, p, task).Version
			set, err := s.SetSummary(ctx, p, task, "first", "alice", v)
			noErr(t, err)
			if set.Body != "first" || set.UpdatedBy != "alice" || set.UpdatedAt == 0 || set.ProjectID != p || set.TaskID != task {
				t.Fatalf("SetSummary = %+v", set)
			}
			_, err = s.SetSummary(ctx, p, task, "second", "bob", v)
			noErr(t, err)
			got, err := s.GetSummary(ctx, p, task)
			noErr(t, err)
			if got.Body != "second" || got.UpdatedBy != "bob" {
				t.Fatalf("GetSummary = %+v, want the second", got)
			}
			if after := stateOf(t, sd, p, task).Version; after != v {
				t.Fatalf("the task's version moved %d -> %d: the bump is the caller's", v, after)
			}
		})

		t.Run("AStaleVersionIsStaleAndWritesNothing", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			v := stateOf(t, sd, p, task).Version
			_, err := s.SetSummary(ctx, p, task, "kept", "alice", v)
			noErr(t, err)
			_, err = s.SetSummary(ctx, p, task, "lost", "bob", v+1)
			e := wantErr(t, err, domain.CodeStale, "")
			if e.Details["expected"] != v+1 || e.Details["actual"] != v {
				t.Fatalf("STALE details %v, want expected %d actual %d", e.Details, v+1, v)
			}
			got, err := s.GetSummary(ctx, p, task)
			noErr(t, err)
			if got.Body != "kept" {
				t.Fatalf("after STALE the summary is %q, want kept", got.Body)
			}
		})

		t.Run("AMissingTaskIsNotFound", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.GetSummary(ctx, p, "T-404")
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.SetSummary(ctx, p, "T-404", "x", "alice", 1)
			wantErr(t, err, domain.CodeNotFound, "Task")
		})
	})
}
