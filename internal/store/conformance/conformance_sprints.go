package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("sprints", sprints) }

// sprints is the sprint area: a create reads back PROPOSED at version 1 with an S- id; the board,
// shape and missing-sprint refusals; paging oldest first with board and status filters; the
// compare-and-set update and its STALE; the member count kept at or above 0 without a version move;
// and the member read, by the sprint's board, in (order key, id) order.
func sprints(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListSprints(ctx, "P-probe", store.SprintFilter{}, "", 1)
		return err
	}
	area(t, "Sprints", newStore, probe, func(t *testing.T) {
		create := func(t *testing.T, s store.Store, p, b, name string, at int64) store.Sprint {
			t.Helper()
			sp, err := s.CreateSprint(ctx, store.CreateSprintInput{ProjectID: p, BoardID: b, Name: name, Goal: "g", Acceptance: "a", CreatedBy: seedIdentity, CreatedAt: at})
			noErr(t, err)
			return sp
		}
		now := time.Now().UnixMilli()

		t.Run("ACreateReadsBack", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			sp := create(t, s, p, b, "first", now)
			if !strings.HasPrefix(sp.ID, "S-") || len(sp.ID) != 14 || sp.Status != domain.SprintProposed || sp.Version != 1 || sp.MemberCount != 0 ||
				sp.ProposalTaskID != nil || sp.StartedAt != nil || sp.UpdatedBy != seedIdentity || sp.UpdatedAt != now {
				t.Fatalf("created %+v, want S-<12 hex>, PROPOSED, v1, no members, nothing bound", sp)
			}
			got, err := s.GetSprint(ctx, p, sp.ID)
			noErr(t, err)
			if got != sp {
				t.Fatalf("GetSprint = %+v, want %+v", got, sp)
			}
		})

		t.Run("RefusalsHaveOneShape", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			_, err := s.CreateSprint(ctx, store.CreateSprintInput{ProjectID: p, BoardID: "B-404", Name: "n", Goal: "g", Acceptance: "a", CreatedBy: seedIdentity, CreatedAt: now})
			wantErr(t, err, domain.CodeNotFound, "Board")
			_, err = s.CreateSprint(ctx, store.CreateSprintInput{ProjectID: p, BoardID: b, Name: "n", Goal: "", Acceptance: "a", CreatedBy: seedIdentity, CreatedAt: now})
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.GetSprint(ctx, p, "S-404")
			wantErr(t, err, domain.CodeNotFound, "Sprint")
			_, err = s.UpdateSprint(ctx, p, "S-404", 1, store.SprintPatch{UpdatedBy: seedIdentity, UpdatedAt: now})
			wantErr(t, err, domain.CodeNotFound, "Sprint")
			_, err = s.AddSprintMembers(ctx, p, "S-404", 1)
			wantErr(t, err, domain.CodeNotFound, "Sprint")
			bad := domain.SprintStatus("OPEN")
			_, err = s.ListSprints(ctx, p, store.SprintFilter{Status: &bad}, "", 10)
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.ListSprints(ctx, p, store.SprintFilter{}, "not-a-cursor", 10)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("PagesOldestFirstWithFilters", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			b2, err := sd.seedBoard(ctx, p)
			noErr(t, err)
			a := create(t, s, p, b, "a", now)
			c := create(t, s, p, b2, "c", now+2)
			bb := create(t, s, p, b, "b", now+1)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			create(t, s, q, qb, "other project", now)
			var ids []string
			cursor := ""
			for range 4 {
				page, err := s.ListSprints(ctx, p, store.SprintFilter{}, cursor, 1)
				noErr(t, err)
				for _, sp := range page.Sprints {
					ids = append(ids, sp.Name)
				}
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
			if strings.Join(ids, " ") != "a b c" {
				t.Fatalf("paged %v, want a b c oldest first", ids)
			}
			page, err := s.ListSprints(ctx, p, store.SprintFilter{BoardID: b2}, "", 10)
			noErr(t, err)
			if len(page.Sprints) != 1 || page.Sprints[0].ID != c.ID || page.NextCursor != "" {
				t.Fatalf("board filter = %+v, want only %s", page, c.ID)
			}
			active := domain.SprintActive
			if _, err := s.UpdateSprint(ctx, p, bb.ID, 1, store.SprintPatch{Status: &active, UpdatedBy: seedIdentity, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			page, err = s.ListSprints(ctx, p, store.SprintFilter{Status: &active}, "", 10)
			noErr(t, err)
			if len(page.Sprints) != 1 || page.Sprints[0].ID != bb.ID {
				t.Fatalf("status filter = %+v, want only %s", page.Sprints, bb.ID)
			}
			_ = a
		})

		t.Run("UpdateIsCompareAndSet", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			sp := create(t, s, p, b, "x", now)
			name, task, at := "renamed", "T-9", now+10
			active := domain.SprintActive
			got, err := s.UpdateSprint(ctx, p, sp.ID, 1, store.SprintPatch{Name: &name, Status: &active, ProposalTaskID: &store.Nullable[string]{Value: &task},
				ApprovedBy: &store.Nullable[string]{Value: &task}, StartedAt: &store.Nullable[int64]{Value: &at}, Checkpoint: &store.Nullable[string]{Value: &name},
				UpdatedBy: "bob", UpdatedAt: at})
			noErr(t, err)
			if got.Version != 2 || got.Name != name || got.Status != active || got.ProposalTaskID == nil || *got.ProposalTaskID != task ||
				got.StartedAt == nil || *got.StartedAt != at || got.UpdatedBy != "bob" || got.Goal != "g" || got.Checkpoint == nil {
				t.Fatalf("updated %+v, want v2 with the patch applied and the rest kept", got)
			}
			cleared, err := s.UpdateSprint(ctx, p, sp.ID, 2, store.SprintPatch{Checkpoint: &store.Nullable[string]{}, UpdatedBy: "bob", UpdatedAt: at})
			noErr(t, err)
			if cleared.Checkpoint != nil || cleared.ProposalTaskID == nil {
				t.Fatalf("a nil Value cleared %v and kept %v, want nil and the binding", cleared.Checkpoint, cleared.ProposalTaskID)
			}
			_, err = s.UpdateSprint(ctx, p, sp.ID, 2, store.SprintPatch{Name: &name, UpdatedBy: "bob", UpdatedAt: at})
			e := wantErr(t, err, domain.CodeStale, "")
			if e.Details["actual"] != int64(3) && e.Details["actual"] != 3 {
				t.Fatalf("STALE details %v, want actual 3", e.Details)
			}
			empty := ""
			_, err = s.UpdateSprint(ctx, p, sp.ID, 3, store.SprintPatch{Name: &empty, UpdatedBy: "bob", UpdatedAt: at})
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("MemberCountMovesWithoutAVersion", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			sp := create(t, s, p, b, "x", now)
			got, err := s.AddSprintMembers(ctx, p, sp.ID, 2)
			noErr(t, err)
			if got.MemberCount != 2 || got.Version != 1 {
				t.Fatalf("after +2: %d members v%d, want 2 at v1", got.MemberCount, got.Version)
			}
			got, err = s.AddSprintMembers(ctx, p, sp.ID, -5)
			noErr(t, err)
			if got.MemberCount != 0 {
				t.Fatalf("after -5: %d members, want 0 (never below)", got.MemberCount)
			}
		})

		t.Run("MembersAreTheSprintBoardsTasksHoldingIt", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			sp := create(t, s, p, b, "x", now)
			other := create(t, s, p, b, "y", now)
			ids := seedTasks(t, sd, p, b, "one", "two", "three", "four")
			stamp := func(id string, sprints ...string) {
				cur, err := s.GetTask(ctx, p, id)
				noErr(t, err)
				_, err = s.UpdateTask(ctx, p, id, cur.Version, store.TaskPatch{SprintIDs: &sprints, UpdatedBy: seedIdentity, UpdatedAt: now})
				noErr(t, err)
			}
			stamp(ids[2], sp.ID)
			stamp(ids[0], other.ID, sp.ID)
			stamp(ids[1], other.ID)
			got, err := s.ListSprintMembers(ctx, p, sp.ID, 0)
			noErr(t, err)
			if len(got) != 2 || got[0].ID != ids[0] || got[1].ID != ids[2] {
				t.Fatalf("members = %v, want %s and %s", taskIDs(got), ids[0], ids[2])
			}
			one, err := s.ListSprintMembers(ctx, p, sp.ID, 1)
			noErr(t, err)
			if len(one) != 1 {
				t.Fatalf("limit 1 returned %d", len(one))
			}
			none, err := s.ListSprintMembers(ctx, p, "S-404", 0)
			noErr(t, err)
			if none == nil || len(none) != 0 {
				t.Fatalf("an unknown sprint's members = %#v, want an empty list", none)
			}
		})
	})
}
