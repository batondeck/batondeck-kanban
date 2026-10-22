package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("dependencies", dependencies) }

// dependencies is the dependency area: an edge and its two caches written together, a refused
// cycle leaving no partial edge or cache entry (alone and inside Atomically), relates edges, the
// repeated edge, removal, the lists, SetCaches, project isolation and the refusals.
func dependencies(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListEdges(ctx, "P-probe")
		return err
	}
	area(t, "Dependencies", newStore, probe, func(t *testing.T) {
		blocks := func(p, from, to string) store.AddEdgeInput {
			return store.AddEdgeInput{ProjectID: p, FromTaskID: from, ToTaskID: to, Type: store.DependencyBlocks, CreatedBy: "alice"}
		}
		edgeIDs := func(t *testing.T, s store.Store, p string) [][2]string {
			t.Helper()
			es, err := s.ListEdges(ctx, p)
			noErr(t, err)
			out := make([][2]string, len(es))
			for i, e := range es {
				out[i] = [2]string{e.FromTaskID, e.ToTaskID}
			}
			return out
		}
		// versions is each task's version: no Dependencies call moves it, the caller bumps once per
		// mutation (service.mutate), so a battery compares the versions before and after its calls.
		versions := func(t *testing.T, sd seeder, p string, tasks ...string) []int64 {
			t.Helper()
			out := make([]int64, len(tasks))
			for i, task := range tasks {
				out[i] = stateOf(t, sd, p, task).Version
			}
			return out
		}
		unbumped := func(t *testing.T, sd seeder, p string, before []int64, tasks ...string) {
			t.Helper()
			if after := versions(t, sd, p, tasks...); !slices.Equal(after, before) {
				t.Fatalf("versions of %v moved %v -> %v across Dependencies calls: the bump is the caller's", tasks, before, after)
			}
		}
		wantCaches := func(t *testing.T, sd seeder, p, task string, blockedBy, blocksList []string) {
			t.Helper()
			st := stateOf(t, sd, p, task)
			if !slices.Equal(st.BlockedBy, blockedBy) || !slices.Equal(st.Blocks, blocksList) {
				t.Fatalf("%s caches blockedBy %v blocks %v, want %v and %v", task, st.BlockedBy, st.Blocks, blockedBy, blocksList)
			}
		}

		t.Run("AddEdgeWritesTheEdgeAndBothCaches", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b")
			before := versions(t, sd, p, ts...)
			e, err := s.AddEdge(ctx, blocks(p, ts[0], ts[1]))
			noErr(t, err)
			if e.ID == "" || e.TS == 0 || e.ProjectID != p || e.FromTaskID != ts[0] || e.ToTaskID != ts[1] || e.Type != store.DependencyBlocks || e.CreatedBy != "alice" {
				t.Fatalf("AddEdge = %+v", e)
			}
			wantCaches(t, sd, p, ts[0], []string{}, []string{ts[1]})
			wantCaches(t, sd, p, ts[1], []string{ts[0]}, []string{})
			unbumped(t, sd, p, before, ts...)
			blockers, err := s.ListBlockers(ctx, p, ts[1])
			noErr(t, err)
			dependents, err := s.ListDependents(ctx, p, ts[0])
			noErr(t, err)
			if len(blockers) != 1 || blockers[0].ID != e.ID || len(dependents) != 1 || dependents[0].ID != e.ID {
				t.Fatalf("blockers of %s %+v, dependents of %s %+v; want the edge in each", ts[1], blockers, ts[0], dependents)
			}
			none, err := s.ListBlockers(ctx, p, ts[0])
			noErr(t, err)
			if len(none) != 0 {
				t.Fatalf("blockers of %s %+v, want none", ts[0], none)
			}
		})

		t.Run("ARefusedCycleLeavesNoPartialEdgeOrCacheEntry", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b", "c")
			before := versions(t, sd, p, ts...)
			a, bb, c := ts[0], ts[1], ts[2]
			_, err := s.AddEdge(ctx, blocks(p, a, bb))
			noErr(t, err)
			_, err = s.AddEdge(ctx, blocks(p, bb, a))
			e := wantErr(t, err, domain.CodeCycleDetected, "")
			if e.Details["fromTaskId"] != bb || e.Details["toTaskId"] != a {
				t.Fatalf("CYCLE_DETECTED details %v, want from %s to %s", e.Details, bb, a)
			}
			_, err = s.AddEdge(ctx, blocks(p, a, a))
			wantErr(t, err, domain.CodeCycleDetected, "")
			_, err = s.AddEdge(ctx, blocks(p, bb, c))
			noErr(t, err)
			_, err = s.AddEdge(ctx, blocks(p, c, a))
			wantErr(t, err, domain.CodeCycleDetected, "")
			if got := edgeIDs(t, s, p); !slices.Equal(got, [][2]string{{a, bb}, {bb, c}}) {
				t.Fatalf("edges after the refusals %v, want [%s->%s %s->%s]", got, a, bb, bb, c)
			}
			wantCaches(t, sd, p, a, []string{}, []string{bb})
			wantCaches(t, sd, p, bb, []string{a}, []string{c})
			wantCaches(t, sd, p, c, []string{bb}, []string{})
			unbumped(t, sd, p, before, ts...)
		})

		t.Run("ARefusalInsideAtomicallyRollsBackTheEdgesBeforeIt", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "c", "d")
			err := s.Atomically(ctx, func(tx store.Store) error {
				if _, err := tx.AddEdge(ctx, blocks(p, ts[0], ts[1])); err != nil {
					return err
				}
				_, err := tx.AddEdge(ctx, blocks(p, ts[1], ts[0]))
				return err
			})
			wantErr(t, err, domain.CodeCycleDetected, "")
			if got := edgeIDs(t, s, p); len(got) != 0 {
				t.Fatalf("edges after the rolled-back transaction %v, want none", got)
			}
			wantCaches(t, sd, p, ts[0], []string{}, []string{})
			wantCaches(t, sd, p, ts[1], []string{}, []string{})
		})

		t.Run("RelatesEdgesSkipTheCycleCheckAndTheCaches", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b")
			for _, pair := range [][2]string{{ts[0], ts[1]}, {ts[1], ts[0]}} {
				_, err := s.AddEdge(ctx, store.AddEdgeInput{ProjectID: p, FromTaskID: pair[0], ToTaskID: pair[1], Type: store.DependencyRelates, CreatedBy: "alice"})
				noErr(t, err)
			}
			if got := edgeIDs(t, s, p); len(got) != 2 {
				t.Fatalf("edges %v, want both relates edges", got)
			}
			wantCaches(t, sd, p, ts[0], []string{}, []string{})
			wantCaches(t, sd, p, ts[1], []string{}, []string{})
		})

		t.Run("AddingTheSameEdgeAgainReturnsTheFirst", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b")
			first, err := s.AddEdge(ctx, blocks(p, ts[0], ts[1]))
			noErr(t, err)
			again, err := s.AddEdge(ctx, blocks(p, ts[0], ts[1]))
			noErr(t, err)
			if again.ID != first.ID || len(edgeIDs(t, s, p)) != 1 {
				t.Fatalf("the repeat returned %s (first %s) and the project holds %d edges", again.ID, first.ID, len(edgeIDs(t, s, p)))
			}
			wantCaches(t, sd, p, ts[1], []string{ts[0]}, []string{})
		})

		t.Run("RemoveEdgeRemovesTheEdgeAndItsCacheEntries", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b", "c")
			before := versions(t, sd, p, ts...)
			ab, err := s.AddEdge(ctx, blocks(p, ts[0], ts[1]))
			noErr(t, err)
			_, err = s.AddEdge(ctx, blocks(p, ts[2], ts[1]))
			noErr(t, err)
			removed, err := s.RemoveEdge(ctx, p, ab.ID)
			noErr(t, err)
			if removed.ID != ab.ID || removed.FromTaskID != ts[0] || removed.ToTaskID != ts[1] {
				t.Fatalf("RemoveEdge returned %+v, want %+v", removed, ab)
			}
			if got := edgeIDs(t, s, p); !slices.Equal(got, [][2]string{{ts[2], ts[1]}}) {
				t.Fatalf("edges %v, want the other one only", got)
			}
			wantCaches(t, sd, p, ts[0], []string{}, []string{})
			wantCaches(t, sd, p, ts[1], []string{ts[2]}, []string{})
			unbumped(t, sd, p, before, ts...)
			_, err = s.RemoveEdge(ctx, p, ab.ID)
			wantErr(t, err, domain.CodeNotFound, "DependencyEdge")
		})

		t.Run("SetCachesReplacesBothInOrder", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			before := versions(t, sd, p, task)
			noErr(t, s.SetCaches(ctx, p, task, []string{"T-9", "T-7"}, []string{"T-8"}))
			wantCaches(t, sd, p, task, []string{"T-9", "T-7"}, []string{"T-8"})
			noErr(t, s.SetCaches(ctx, p, task, nil, nil))
			wantCaches(t, sd, p, task, []string{}, []string{})
			unbumped(t, sd, p, before, task)
			wantErr(t, s.SetCaches(ctx, p, "T-404", nil, nil), domain.CodeNotFound, "Task")
		})

		t.Run("EdgesAreScopedByTheirProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "a", "b")
			qt := seedTasks(t, sd, q, qb, "a", "b")
			_, err = s.AddEdge(ctx, blocks(p, pt[0], pt[1]))
			noErr(t, err)
			if got := edgeIDs(t, s, q); len(got) != 0 {
				t.Fatalf("%s lists %v: another project's edge leaked", q, got)
			}
			dependents, err := s.ListDependents(ctx, q, qt[0])
			noErr(t, err)
			if len(dependents) != 0 {
				t.Fatalf("%s/%s dependents %+v: another project's edge leaked", q, qt[0], dependents)
			}
			_, err = s.AddEdge(ctx, blocks(q, qt[1], qt[0]))
			noErr(t, err)
			wantCaches(t, sd, q, qt[0], []string{qt[1]}, []string{})
			wantCaches(t, sd, p, pt[0], []string{}, []string{pt[1]})
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.AddEdge(ctx, blocks(p, "T-404", task))
			e := wantErr(t, err, domain.CodeNotFound, "Task")
			if e.Details["id"] != "T-404" {
				t.Fatalf("NOT_FOUND names %v, want T-404", e.Details["id"])
			}
			_, err = s.AddEdge(ctx, blocks(p, task, "T-405"))
			e = wantErr(t, err, domain.CodeNotFound, "Task")
			if e.Details["id"] != "T-405" {
				t.Fatalf("NOT_FOUND names %v, want T-405", e.Details["id"])
			}
			_, err = s.AddEdge(ctx, store.AddEdgeInput{ProjectID: p, FromTaskID: task, ToTaskID: task, Type: "duplicates", CreatedBy: "alice"})
			wantErr(t, err, domain.CodeValidation, "")
			other := seedTasks(t, sd, p, b, "b")[0]
			_, err = s.AddEdge(ctx, store.AddEdgeInput{ProjectID: p, FromTaskID: task, ToTaskID: other, Type: store.DependencyBlocks})
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.ListEdges(ctx, "P-missing")
			wantErr(t, err, domain.CodeNotFound, "Project")
			for _, call := range []func() error{
				func() error { _, err := s.ListBlockers(ctx, p, "T-404"); return err },
				func() error { _, err := s.ListDependents(ctx, p, "T-404"); return err },
			} {
				wantErr(t, call(), domain.CodeNotFound, "Task")
			}
			if got := edgeIDs(t, s, p); len(got) != 0 {
				t.Fatalf("refused adds left %v", got)
			}
		})
	})
}
