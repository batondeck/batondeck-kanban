package memstore

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func cloneSprint(sp store.Sprint) store.Sprint {
	sp.ProposalTaskID = clonePtr(sp.ProposalTaskID)
	sp.ApprovedBy = clonePtr(sp.ApprovedBy)
	sp.Checkpoint = clonePtr(sp.Checkpoint)
	sp.EventCursor = clonePtr(sp.EventCursor)
	sp.StartedAt = clonePtr(sp.StartedAt)
	sp.ClosedAt = clonePtr(sp.ClosedAt)
	return sp
}

func (st *state) sprint(projectID, sprintID string) (store.Sprint, error) {
	sp, ok := st.sprints[[2]string{projectID, sprintID}]
	if !ok {
		return store.Sprint{}, domain.NotFound("Sprint", sprintID)
	}
	return cloneSprint(sp), nil
}

func (s *Store) CreateSprint(ctx context.Context, in store.CreateSprintInput) (store.Sprint, error) {
	if err := store.CheckCreateSprint(in); err != nil {
		return store.Sprint{}, err
	}
	return get(ctx, s, func(st *state) (store.Sprint, error) {
		if _, ok := st.boards[[2]string{in.ProjectID, in.BoardID}]; !ok {
			return store.Sprint{}, domain.NotFound("Board", in.BoardID)
		}
		sp := store.Sprint{
			ProjectID: in.ProjectID, ID: store.NewSprintID(), BoardID: in.BoardID, Name: in.Name, Goal: in.Goal,
			Acceptance: in.Acceptance, Status: domain.SprintProposed, Version: 1, CreatedBy: in.CreatedBy,
			CreatedAt: in.CreatedAt, UpdatedBy: in.CreatedBy, UpdatedAt: in.CreatedAt,
		}
		st.sprints[[2]string{sp.ProjectID, sp.ID}] = sp
		return cloneSprint(sp), nil
	})
}

func (s *Store) GetSprint(ctx context.Context, projectID, sprintID string) (store.Sprint, error) {
	return get(ctx, s, func(st *state) (store.Sprint, error) { return st.sprint(projectID, sprintID) })
}

func (s *Store) ListSprints(ctx context.Context, projectID string, f store.SprintFilter, cursor string, limit int) (store.SprintPage, error) {
	afterCreated, afterID, err := store.SprintCursor(cursor)
	if err != nil {
		return store.SprintPage{}, err
	}
	if f.Status != nil {
		if _, err := domain.ParseSprintStatus(string(*f.Status)); err != nil {
			return store.SprintPage{}, err
		}
	}
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) (store.SprintPage, error) {
		var all []store.Sprint
		for k, sp := range st.sprints {
			switch {
			case k[0] != projectID, f.BoardID != "" && sp.BoardID != f.BoardID, f.Status != nil && sp.Status != *f.Status:
				continue
			case afterID != "" && (sp.CreatedAt < afterCreated || sp.CreatedAt == afterCreated && sp.ID <= afterID):
				continue
			}
			all = append(all, cloneSprint(sp))
		}
		slices.SortFunc(all, func(a, b store.Sprint) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), strings.Compare(a.ID, b.ID))
		})
		page := store.SprintPage{Sprints: []store.Sprint{}}
		for i, sp := range all {
			if i == limit {
				last := page.Sprints[limit-1]
				page.NextCursor = store.EncodeCursor(strconv.FormatInt(last.CreatedAt, 10), last.ID)
				break
			}
			page.Sprints = append(page.Sprints, sp)
		}
		return page, nil
	})
}

func (s *Store) UpdateSprint(ctx context.Context, projectID, sprintID string, expectedVersion int64, p store.SprintPatch) (store.Sprint, error) {
	if err := store.CheckSprintPatch(p); err != nil {
		return store.Sprint{}, err
	}
	return get(ctx, s, func(st *state) (store.Sprint, error) {
		cur, err := st.sprint(projectID, sprintID)
		if err != nil {
			return store.Sprint{}, err
		}
		if cur.Version != expectedVersion {
			return store.Sprint{}, domain.Stale(expectedVersion, cur.Version)
		}
		next := store.ApplySprintPatch(cur, p)
		next.Version++
		st.sprints[[2]string{projectID, sprintID}] = cloneSprint(next)
		return next, nil
	})
}

func (s *Store) AddSprintMembers(ctx context.Context, projectID, sprintID string, delta int64) (store.Sprint, error) {
	return get(ctx, s, func(st *state) (store.Sprint, error) {
		sp, err := st.sprint(projectID, sprintID)
		if err != nil {
			return store.Sprint{}, err
		}
		sp.MemberCount = max(0, sp.MemberCount+delta)
		st.sprints[[2]string{projectID, sprintID}] = cloneSprint(sp)
		return sp, nil
	})
}

func (s *Store) ListSprintMembers(ctx context.Context, projectID, sprintID string, limit int) ([]store.Task, error) {
	limit = clamp(limit, 500, 500)
	return get(ctx, s, func(st *state) ([]store.Task, error) {
		out := []store.Task{}
		sp, ok := st.sprints[[2]string{projectID, sprintID}]
		if !ok {
			return out, nil
		}
		for k, t := range st.tasks {
			if k[0] == projectID && t.BoardID == sp.BoardID && slices.Contains(t.SprintIDs, sprintID) {
				out = append(out, cloneTask(t))
			}
		}
		slices.SortFunc(out, byOrderKey)
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	})
}
