package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("runs", runs) }

// runs is the race bookkeeping: run rows in ordinal order, a duplicate and a missing run task
// refused, project isolation, the row leaving with its run task, and SetStatus writing the race flag
// with the status in one version.
func runs(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListRuns(ctx, "P-probe", "T-1")
		return err
	}
	area(t, "Runs", newStore, probe, func(t *testing.T) {
		now := time.Now().UnixMilli()
		run := func(t *testing.T, s store.Store, p, b, parent string, ordinal int64) store.Task {
			t.Helper()
			pt, err := s.GetTask(ctx, p, parent)
			noErr(t, err)
			r, err := s.CreateTask(ctx, store.CreateTaskInput{ProjectID: p, BoardID: b, ColumnID: pt.ColumnID, Title: "run", Status: domain.Todo,
				OrderKey: "a0", RunOf: &parent, CreatedBy: seedIdentity, CreatedAt: now})
			noErr(t, err)
			noErr(t, s.InsertRun(ctx, store.Run{ProjectID: p, RunTaskID: r.ID, ParentTaskID: parent, Ordinal: ordinal, OpenedBy: seedIdentity, OpenedAt: now}))
			return r
		}

		t.Run("RowsAreInOrdinalOrder", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			parent := seedTasks(t, sd, p, b, "parent")[0]
			r3, r1, r2 := run(t, s, p, b, parent, 3), run(t, s, p, b, parent, 1), run(t, s, p, b, parent, 2)
			got, err := s.ListRuns(ctx, p, parent)
			noErr(t, err)
			if len(got) != 3 || got[0].RunTaskID != r1.ID || got[1].RunTaskID != r2.ID || got[2].RunTaskID != r3.ID {
				t.Fatalf("ListRuns = %+v, want %s %s %s by ordinal", got, r1.ID, r2.ID, r3.ID)
			}
			if g := got[0]; g.ParentTaskID != parent || g.Ordinal != 1 || g.OpenedBy != seedIdentity || g.OpenedAt != now || g.ProjectID != p {
				t.Fatalf("row %+v does not read back as written", g)
			}
		})

		t.Run("AParentWithNoneListsNone", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			lone := seedTasks(t, sd, p, b, "lone")[0]
			for _, id := range []string{lone, "T-404"} {
				got, err := s.ListRuns(ctx, p, id)
				noErr(t, err)
				if got == nil || len(got) != 0 {
					t.Fatalf("ListRuns(%s) = %#v, want an empty list", id, got)
				}
			}
		})

		t.Run("ADuplicateAMissingTaskAndAMalformedRowAreRefused", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			parent := seedTasks(t, sd, p, b, "parent")[0]
			r := run(t, s, p, b, parent, 1)
			wantErr(t, s.InsertRun(ctx, store.Run{ProjectID: p, RunTaskID: r.ID, ParentTaskID: parent, Ordinal: 2, OpenedBy: seedIdentity, OpenedAt: now}), domain.CodeValidation, "")
			wantErr(t, s.InsertRun(ctx, store.Run{ProjectID: p, RunTaskID: "T-404", ParentTaskID: parent, Ordinal: 2, OpenedBy: seedIdentity, OpenedAt: now}), domain.CodeNotFound, "Task")
			wantErr(t, s.InsertRun(ctx, store.Run{ProjectID: p, RunTaskID: r.ID, ParentTaskID: parent, Ordinal: 0, OpenedBy: seedIdentity, OpenedAt: now}), domain.CodeValidation, "")
			got, err := s.ListRuns(ctx, p, parent)
			noErr(t, err)
			if len(got) != 1 || got[0].Ordinal != 1 {
				t.Fatalf("after the refusals ListRuns = %+v, want the one row at ordinal 1", got)
			}
		})

		t.Run("RowsAreScopedByTheirProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pp := seedTasks(t, sd, p, b, "parent")[0]
			run(t, s, p, b, pp, 1)
			qp := seedTasks(t, sd, q, qb, "parent")[0]
			if qp != pp {
				t.Fatalf("parents %s and %s: the probe needs the same T-n in both projects", pp, qp)
			}
			got, err := s.ListRuns(ctx, q, qp)
			noErr(t, err)
			if len(got) != 0 {
				t.Fatalf("%s/%s lists %+v: another project's runs leaked", q, qp, got)
			}
		})

		t.Run("TheRowLeavesWithItsRunTask", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			parent := seedTasks(t, sd, p, b, "parent")[0]
			r1, r2 := run(t, s, p, b, parent, 1), run(t, s, p, b, parent, 2)
			noErr(t, s.DeleteTask(ctx, p, r1.ID, r1.Version))
			got, err := s.ListRuns(ctx, p, parent)
			noErr(t, err)
			if len(got) != 1 || got[0].RunTaskID != r2.ID {
				t.Fatalf("after deleting %s ListRuns = %+v, want only %s", r1.ID, got, r2.ID)
			}
		})

		t.Run("SetStatusWritesTheRaceFlagInOneVersion", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			id := seedTasks(t, sd, p, b, "parent")[0]
			cur, err := s.GetTask(ctx, p, id)
			noErr(t, err)
			on, off := true, false
			at := now - 5
			got, err := s.SetStatus(ctx, p, id, cur.Version, store.StatusChange{Status: domain.Todo, Racing: &on,
				RaceOpenedAt: &store.Nullable[int64]{Value: &at}, UpdatedBy: seedIdentity, UpdatedAt: now})
			noErr(t, err)
			if !got.Racing || got.RaceOpenedAt == nil || *got.RaceOpenedAt != at || got.Version != cur.Version+1 {
				t.Fatalf("SetStatus with the race flag: racing %v at %v v%d, want true, %d, v%d", got.Racing, got.RaceOpenedAt, got.Version, at, cur.Version+1)
			}
			kept, err := s.SetStatus(ctx, p, id, got.Version, store.StatusChange{Status: domain.Todo, UpdatedBy: seedIdentity, UpdatedAt: now})
			noErr(t, err)
			if !kept.Racing || kept.RaceOpenedAt == nil {
				t.Fatalf("SetStatus without the race flag changed it: racing %v at %v", kept.Racing, kept.RaceOpenedAt)
			}
			cleared, err := s.SetStatus(ctx, p, id, kept.Version, store.StatusChange{Status: domain.Done, Racing: &off,
				RaceOpenedAt: &store.Nullable[int64]{}, UpdatedBy: seedIdentity, UpdatedAt: now})
			noErr(t, err)
			if re, err := s.GetTask(ctx, p, id); err != nil || re.Racing || re.RaceOpenedAt != nil || re.Status != domain.Done || cleared.Racing {
				t.Fatalf("cleared: racing %v at %v status %s (%v), want false, nil, DONE", re.Racing, re.RaceOpenedAt, re.Status, err)
			}
		})
	})
}
