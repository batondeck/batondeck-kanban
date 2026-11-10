package memstore

import (
	"context"
	"slices"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The artifact, comment and follow-up areas of contract_{artifacts,comments,followups}.go.

func (s *Store) AddArtifact(ctx context.Context, in store.AddArtifactInput) (store.Artifact, error) {
	if err := store.CheckArtifactInput(in); err != nil {
		return store.Artifact{}, err
	}
	return get(ctx, s, func(st *state) (store.Artifact, error) {
		if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
			return store.Artifact{}, err
		}
		a := store.Artifact{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("AR-", 16), Kind: in.Kind, URL: clonePtr(in.URL),
			Ref: clonePtr(in.Ref), Repo: clonePtr(in.Repo), Title: clonePtr(in.Title), AddedBy: in.AddedBy,
			AddedByAgent: clonePtr(in.AddedByAgent), TS: nowMillis()}
		st.artifacts[[3]string{a.ProjectID, a.TaskID, a.ID}] = a
		return a, nil
	})
}

func (s *Store) ListArtifacts(ctx context.Context, projectID, taskID string) ([]store.Artifact, error) {
	return get(ctx, s, func(st *state) ([]store.Artifact, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return nil, err
		}
		out := []store.Artifact{}
		for k, a := range st.artifacts {
			if k[0] == projectID && k[1] == taskID {
				out = append(out, a)
			}
		}
		slices.SortFunc(out, func(a, b store.Artifact) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		return out, nil
	})
}

func (s *Store) RetractArtifact(ctx context.Context, projectID, taskID, id string, r store.ArtifactRetraction) (store.Artifact, error) {
	if r.By == "" || r.Reason == "" {
		return store.Artifact{}, domain.Validation("a retraction needs a by and a reason", nil)
	}
	return get(ctx, s, func(st *state) (store.Artifact, error) {
		k := [3]string{projectID, taskID, id}
		a, ok := st.artifacts[k]
		if !ok || a.Retraction != nil {
			return store.Artifact{}, domain.NotFound("Artifact", id)
		}
		rr := r
		rr.ByAgent = clonePtr(r.ByAgent)
		a.Retraction = &rr
		st.artifacts[k] = a
		return a, nil
	})
}

func (s *Store) LiveArtifactTasks(ctx context.Context, projectID string, taskIDs []string) (map[string]bool, error) {
	return get(ctx, s, func(st *state) (map[string]bool, error) {
		out := map[string]bool{}
		for k, a := range st.artifacts {
			if k[0] == projectID && a.Retraction == nil && slices.Contains(taskIDs, k[1]) {
				out[k[1]] = true
			}
		}
		return out, nil
	})
}

func (s *Store) AddComment(ctx context.Context, in store.AddCommentInput) (store.Comment, error) {
	switch {
	case in.Body == "":
		return store.Comment{}, domain.Validation("a comment needs a body", nil)
	case in.Author == "":
		return store.Comment{}, domain.Validation("a comment needs an author", nil)
	}
	return get(ctx, s, func(st *state) (store.Comment, error) {
		if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
			return store.Comment{}, err
		}
		if p := in.ParentCommentID; p != nil {
			if _, ok := st.comments[[3]string{in.ProjectID, in.TaskID, *p}]; !ok {
				return store.Comment{}, domain.NotFound("Comment", *p)
			}
		}
		mentions := cloneStrings(in.Mentions)
		if mentions == nil {
			mentions = []string{}
		}
		c := store.Comment{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("CM-", 16), Author: in.Author, Body: in.Body,
			ParentCommentID: clonePtr(in.ParentCommentID), Mentions: mentions, TS: nowMillis()}
		st.comments[[3]string{c.ProjectID, c.TaskID, c.ID}] = c
		c.Mentions = cloneStrings(c.Mentions)
		return c, nil
	})
}

func (s *Store) ListComments(ctx context.Context, projectID, taskID string, q store.CommentsQuery) (store.CommentsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return store.CommentsPage{}, err
	}
	limit := clamp(q.Limit, 100, 200)
	return get(ctx, s, func(st *state) (store.CommentsPage, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return store.CommentsPage{}, err
		}
		var all []store.Comment
		for k, c := range st.comments {
			if k[0] == projectID && k[1] == taskID && (afterID == "" || byTSAndID(c.TS, afterTS, c.ID, afterID) > 0) {
				c.Mentions = cloneStrings(c.Mentions)
				all = append(all, c)
			}
		}
		slices.SortFunc(all, func(a, b store.Comment) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		page := store.CommentsPage{Items: []store.Comment{}}
		for _, c := range all {
			if len(page.Items) == limit {
				last := page.Items[limit-1]
				page.NextCursor = tsCursorOf(last.TS, last.ID)
				break
			}
			page.Items = append(page.Items, c)
		}
		return page, nil
	})
}

func (s *Store) AddFollowUp(ctx context.Context, in store.AddFollowUpInput) (store.FollowUp, error) {
	switch {
	case in.Body == "":
		return store.FollowUp{}, domain.Validation("a follow-up needs a body", nil)
	case in.Author == "":
		return store.FollowUp{}, domain.Validation("a follow-up needs an author", nil)
	}
	return get(ctx, s, func(st *state) (store.FollowUp, error) {
		if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
			return store.FollowUp{}, err
		}
		f := store.FollowUp{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("FU-", 16), Body: in.Body, Author: in.Author,
			Target: clonePtr(in.Target), TargetIdentity: clonePtr(in.TargetIdentity), Status: store.FollowUpOpen, Reopened: in.Reopened, TS: nowMillis()}
		st.followUps[[3]string{f.ProjectID, f.TaskID, f.ID}] = f
		return f, nil
	})
}

func (s *Store) GetFollowUp(ctx context.Context, projectID, taskID, id string) (store.FollowUp, error) {
	return get(ctx, s, func(st *state) (store.FollowUp, error) {
		f, ok := st.followUps[[3]string{projectID, taskID, id}]
		if !ok {
			return store.FollowUp{}, domain.NotFound("FollowUp", id)
		}
		return f, nil
	})
}

func (s *Store) ListFollowUps(ctx context.Context, projectID, taskID string, q store.FollowUpsQuery) (store.FollowUpsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return store.FollowUpsPage{}, err
	}
	limit := clamp(q.Limit, 100, 200)
	return get(ctx, s, func(st *state) (store.FollowUpsPage, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return store.FollowUpsPage{}, err
		}
		var all []store.FollowUp
		for k, f := range st.followUps {
			if k[0] == projectID && k[1] == taskID && (!q.OpenOnly || f.Status == store.FollowUpOpen) &&
				(afterID == "" || byTSAndID(f.TS, afterTS, f.ID, afterID) > 0) {
				all = append(all, f)
			}
		}
		slices.SortFunc(all, func(a, b store.FollowUp) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		page := store.FollowUpsPage{Items: []store.FollowUp{}}
		for _, f := range all {
			if len(page.Items) == limit {
				last := page.Items[limit-1]
				page.NextCursor = tsCursorOf(last.TS, last.ID)
				break
			}
			page.Items = append(page.Items, f)
		}
		return page, nil
	})
}

func (s *Store) AckFollowUp(ctx context.Context, projectID, taskID, id string) (store.FollowUp, error) {
	return get(ctx, s, func(st *state) (store.FollowUp, error) {
		k := [3]string{projectID, taskID, id}
		f, ok := st.followUps[k]
		if !ok {
			return store.FollowUp{}, domain.NotFound("FollowUp", id)
		}
		f.Status = store.FollowUpAcked
		st.followUps[k] = f
		return f, nil
	})
}

// tsCursor reads a cursor whose order is a Unix-millisecond time; "" is the first page.
func tsCursor(cursor string) (int64, string, error) {
	if cursor == "" {
		return 0, "", nil
	}
	order, id, err := store.DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	ts, perr := strconv.ParseInt(order, 10, 64)
	if perr != nil || id == "" {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

func tsCursorOf(ts int64, id string) string { return store.EncodeCursor(strconv.FormatInt(ts, 10), id) }
