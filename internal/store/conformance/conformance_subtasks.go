package conformance

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("subtasks", subtasks) }

// subtasks is the parent/child read: children in task-number order, none, project isolation and a
// missing parent.
func subtasks(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListSubtasks(ctx, "P-probe", "T-1")
		return err
	}
	area(t, "Subtasks", newStore, probe, func(t *testing.T) {
		child := func(t *testing.T, sd seeder, p, b, parent string) string {
			t.Helper()
			id, err := sd.seedTask(ctx, seedTaskInput{ProjectID: p, BoardID: b, Title: "child", ParentTaskID: parent})
			noErr(t, err)
			return id
		}

		t.Run("ChildrenAreInTaskNumberOrder", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			parent := seedTasks(t, sd, p, b, "parent")[0]
			var want []string
			for range 10 {
				want = append(want, child(t, sd, p, b, parent))
			}
			seedTasks(t, sd, p, b, "unrelated")
			if want[0] != "T-2" || want[9] != "T-11" {
				t.Fatalf("children seeded as %v, want T-2..T-11", want)
			}
			got, err := s.ListSubtasks(ctx, p, parent)
			noErr(t, err)
			if !slices.Equal(got, want) {
				t.Fatalf("ListSubtasks = %v, want %v (T-2 before T-10)", got, want)
			}
		})

		t.Run("AParentWithNoneListsNone", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			lone := seedTasks(t, sd, p, b, "lone")[0]
			got, err := s.ListSubtasks(ctx, p, lone)
			noErr(t, err)
			if len(got) != 0 {
				t.Fatalf("ListSubtasks = %v, want none", got)
			}
		})

		t.Run("AParentIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pp := seedTasks(t, sd, p, b, "parent")[0]
			child(t, sd, p, b, pp)
			qp := seedTasks(t, sd, q, qb, "parent")[0]
			got, err := s.ListSubtasks(ctx, q, qp)
			noErr(t, err)
			if len(got) != 0 {
				t.Fatalf("%s/%s lists %v: another project's children leaked", q, qp, fmt.Sprint(got))
			}
		})

		t.Run("AMissingParentIsNotFound", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			_, err := s.ListSubtasks(ctx, p, "T-404")
			wantErr(t, err, domain.CodeNotFound, "Task")
		})
	})
}
