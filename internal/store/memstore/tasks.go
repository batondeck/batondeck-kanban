package memstore

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The tasks area of contract_tasks.go.

func cloneTask(t store.Task) store.Task {
	t.BlockedReason = clonePtr(t.BlockedReason)
	t.Assignee = clonePtr(t.Assignee)
	t.Labels = cloneStrings(t.Labels)
	t.RequiredCapabilities = cloneStrings(t.RequiredCapabilities)
	t.Estimate = clonePtr(t.Estimate)
	t.DueAt = clonePtr(t.DueAt)
	t.ParentTaskID = clonePtr(t.ParentTaskID)
	t.CustomFields = cloneBytes(t.CustomFields)
	t.BlockedBy = cloneStrings(t.BlockedBy)
	t.Blocks = cloneStrings(t.Blocks)
	t.Deliverable = clonePtr(t.Deliverable)
	t.Reviewer = clonePtr(t.Reviewer)
	t.Review = cloneBytes(t.Review)
	t.Chars = cloneBytes(t.Chars)
	t.ModelHint = cloneBytes(t.ModelHint)
	t.RunOf = clonePtr(t.RunOf)
	t.SprintIDs = cloneStrings(t.SprintIDs)
	t.RaceOpenedAt = clonePtr(t.RaceOpenedAt)
	t.NextEligibleAt = clonePtr(t.NextEligibleAt)
	t.CreatedByAgent = clonePtr(t.CreatedByAgent)
	if t.Claim != nil {
		c := *t.Claim
		c.Agent, c.AgentID = clonePtr(c.Agent), clonePtr(c.AgentID)
		t.Claim = &c
	}
	return t
}

var priorityRank = map[domain.Priority]int{
	domain.PriorityLow: 0, domain.PriorityNormal: 1, domain.PriorityHigh: 2, domain.PriorityUrgent: 3,
}

func validPriority(p domain.Priority) error {
	if _, ok := priorityRank[p]; !ok {
		return domain.Validation("unknown priority: "+string(p), map[string]any{"priority": p})
	}
	return nil
}

// list is a list column as the engines read it back: never nil.
func list(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return cloneStrings(ss)
}

func isObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil
}

// validJSON refuses a JSON column value that is not JSON (nil is NULL, and allowed).
func validJSON(field string, raw json.RawMessage) error {
	if raw != nil && !json.Valid(raw) {
		return domain.Validation(field+" is not valid JSON", map[string]any{"field": field})
	}
	return nil
}

func validCustomFields(raw json.RawMessage) error {
	if raw != nil && !isObject(raw) {
		return domain.Validation("customFields must be a JSON object", nil)
	}
	return nil
}

// taskAt is the task at expectedVersion: NotFound when there is none, Stale when its version is
// another.
func (st *state) taskAt(projectID, taskID string, expectedVersion int64) (store.Task, error) {
	t, err := st.task(projectID, taskID)
	if err != nil {
		return store.Task{}, err
	}
	if t.Version != expectedVersion {
		return store.Task{}, domain.Stale(expectedVersion, t.Version)
	}
	return t, nil
}

func (st *state) task(projectID, taskID string) (store.Task, error) {
	t, ok := st.tasks[[2]string{projectID, taskID}]
	if !ok {
		return store.Task{}, domain.NotFound("Task", taskID)
	}
	return cloneTask(t), nil
}

// put stores t and returns the caller's copy.
func (st *state) put(t store.Task) store.Task {
	st.tasks[[2]string{t.ProjectID, t.ID}] = cloneTask(t)
	return t
}

// columnOn refuses a column id that is not one of the board's.
func (st *state) columnOn(projectID, boardID, columnID string) error {
	b, err := st.boardOf(projectID, boardID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(b.Columns, func(c store.Column) bool { return c.ID == columnID }) {
		return domain.NotFound("Column", columnID)
	}
	return nil
}

func (s *Store) CreateTask(ctx context.Context, in store.CreateTaskInput) (store.Task, error) {
	switch {
	case strings.TrimSpace(in.Title) == "":
		return store.Task{}, domain.Validation("a task needs a title", nil)
	case in.CreatedBy == "":
		return store.Task{}, domain.Validation("a task needs a creator", nil)
	case in.OrderKey == "":
		return store.Task{}, domain.Validation("a task needs an order key", nil)
	case in.CreatedAt <= 0:
		return store.Task{}, domain.Validation("a task needs its creation time", nil)
	}
	if _, err := domain.ParseStatus(string(in.Status)); err != nil {
		return store.Task{}, err
	}
	priority := in.Priority
	if priority == "" {
		priority = domain.PriorityNormal
	}
	if err := validPriority(priority); err != nil {
		return store.Task{}, err
	}
	if err := validCustomFields(in.CustomFields); err != nil {
		return store.Task{}, err
	}
	if err := validJSON("modelHint", in.ModelHint); err != nil {
		return store.Task{}, err
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		if _, ok := st.projects[in.ProjectID]; !ok {
			return store.Task{}, domain.NotFound("Project", in.ProjectID)
		}
		if err := st.columnOn(in.ProjectID, in.BoardID, in.ColumnID); err != nil {
			return store.Task{}, err
		}
		id, err := st.nextTaskID(in.ProjectID)
		if err != nil {
			return store.Task{}, err
		}
		custom := cloneBytes(in.CustomFields)
		if custom == nil {
			custom = json.RawMessage(`{}`)
		}
		return st.put(store.Task{
			ProjectID: in.ProjectID, ID: id, BoardID: in.BoardID, ColumnID: in.ColumnID, Title: in.Title,
			Description: in.Description, Status: in.Status, Priority: priority, Assignee: clonePtr(in.Assignee),
			Labels: list(in.Labels), RequiredCapabilities: list(in.RequiredCapabilities),
			ParentTaskID: clonePtr(in.ParentTaskID), OrderKey: in.OrderKey, CustomFields: custom,
			BlockedBy: []string{}, Blocks: []string{}, ModelHint: cloneBytes(in.ModelHint), RunOf: clonePtr(in.RunOf),
			SprintIDs: list(in.SprintIDs), Version: 1, CreatedBy: in.CreatedBy, CreatedByAgent: clonePtr(in.CreatedByAgent),
			CreatedAt: in.CreatedAt, UpdatedBy: in.CreatedBy, UpdatedAt: in.CreatedAt,
		}), nil
	})
}

func (s *Store) GetTask(ctx context.Context, projectID, taskID string) (store.Task, error) {
	return get(ctx, s, func(st *state) (store.Task, error) { return st.task(projectID, taskID) })
}

// LockTask is GetTask: Atomically already serialises every transaction.
func (s *Store) LockTask(ctx context.Context, projectID, taskID string) (store.Task, error) {
	return s.GetTask(ctx, projectID, taskID)
}

func validWriter(by string, at int64) error {
	if by == "" || at <= 0 {
		return domain.Validation("a write needs who made it and when", nil)
	}
	return nil
}

func validPatch(p store.TaskPatch) error {
	if err := validWriter(p.UpdatedBy, p.UpdatedAt); err != nil {
		return err
	}
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return domain.Validation("a task needs a title", nil)
	}
	if p.Priority != nil {
		if err := validPriority(*p.Priority); err != nil {
			return err
		}
	}
	if p.CustomFields != nil {
		if err := validCustomFields(*p.CustomFields); err != nil {
			return err
		}
	}
	for field, n := range map[string]*store.Nullable[json.RawMessage]{"review": p.Review, "chars": p.Chars, "modelHint": p.ModelHint} {
		if n != nil && n.Value != nil {
			if err := validJSON(field, *n.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func setNullable[T any](dst **T, n *store.Nullable[T]) {
	if n != nil {
		*dst = clonePtr(n.Value)
	}
}

func setNullableJSON(dst *json.RawMessage, n *store.Nullable[json.RawMessage]) {
	if n == nil {
		return
	}
	if n.Value == nil {
		*dst = nil
		return
	}
	*dst = cloneBytes(*n.Value)
}

func (s *Store) UpdateTask(ctx context.Context, projectID, taskID string, expectedVersion int64, p store.TaskPatch) (store.Task, error) {
	if err := validPatch(p); err != nil {
		return store.Task{}, err
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.taskAt(projectID, taskID, expectedVersion)
		if err != nil {
			return store.Task{}, err
		}
		if p.Title != nil {
			t.Title = *p.Title
		}
		if p.Description != nil {
			t.Description = *p.Description
		}
		if p.Priority != nil {
			t.Priority = *p.Priority
		}
		setNullable(&t.Assignee, p.Assignee)
		if p.Labels != nil {
			t.Labels = list(*p.Labels)
		}
		if p.RequiredCapabilities != nil {
			t.RequiredCapabilities = list(*p.RequiredCapabilities)
		}
		setNullable(&t.Estimate, p.Estimate)
		setNullable(&t.DueAt, p.DueAt)
		if p.CustomFields != nil {
			t.CustomFields = cloneBytes(*p.CustomFields)
		}
		setNullable(&t.Deliverable, p.Deliverable)
		setNullable(&t.Reviewer, p.Reviewer)
		setNullableJSON(&t.Review, p.Review)
		setNullableJSON(&t.Chars, p.Chars)
		setNullableJSON(&t.ModelHint, p.ModelHint)
		if p.SprintIDs != nil {
			t.SprintIDs = list(*p.SprintIDs)
		}
		if p.Racing != nil {
			t.Racing = *p.Racing
		}
		setNullable(&t.RaceOpenedAt, p.RaceOpenedAt)
		if p.Score != nil {
			t.Score = *p.Score
		}
		if p.SearchTokens != nil {
			t.SearchTokens = *p.SearchTokens
		}
		t.Version++
		t.UpdatedBy, t.UpdatedAt = p.UpdatedBy, p.UpdatedAt
		return st.put(t), nil
	})
}

func (s *Store) SetStatus(ctx context.Context, projectID, taskID string, expectedVersion int64, c store.StatusChange) (store.Task, error) {
	if _, err := domain.ParseStatus(string(c.Status)); err != nil {
		return store.Task{}, err
	}
	if err := validWriter(c.UpdatedBy, c.UpdatedAt); err != nil {
		return store.Task{}, err
	}
	if c.OrderKey != nil && *c.OrderKey == "" {
		return store.Task{}, domain.Validation("an empty order key", nil)
	}
	if c.Review != nil && c.Review.Value != nil {
		if err := validJSON("review", *c.Review.Value); err != nil {
			return store.Task{}, err
		}
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.taskAt(projectID, taskID, expectedVersion)
		if err != nil {
			return store.Task{}, err
		}
		if c.ColumnID != "" {
			if err := st.columnOn(projectID, t.BoardID, c.ColumnID); err != nil {
				return store.Task{}, err
			}
			t.ColumnID = c.ColumnID
		}
		if c.Unpoison {
			reason := ""
			if t.BlockedReason != nil {
				reason = *t.BlockedReason
			}
			if !domain.IsPoisoned(t.Status, reason) {
				return store.Task{}, domain.Validation("Unpoison on a task that is not poisoned", map[string]any{"taskId": taskID})
			}
			out := domain.Unpoison(t.Labels)
			t.Labels = list(out.Labels)
			t.ClaimAttempts = int64(out.ClaimAttempts)
			t.NextEligibleAt = nil
		}
		t.Status = c.Status
		t.BlockedReason = nil
		if c.Status == domain.Blocked && c.BlockedReason != "" {
			t.BlockedReason = &c.BlockedReason
		}
		if c.OrderKey != nil {
			t.OrderKey = *c.OrderKey
		}
		if c.ClaimAttempts != nil {
			t.ClaimAttempts = *c.ClaimAttempts
		}
		setNullable(&t.NextEligibleAt, c.NextEligibleAt)
		if c.Labels != nil {
			t.Labels = list(c.Labels)
		}
		if c.ClearClaim {
			t.Claim = nil
		}
		setNullable(&t.Assignee, c.Assignee)
		setNullable(&t.Deliverable, c.Deliverable)
		setNullableJSON(&t.Review, c.Review)
		if c.Racing != nil {
			t.Racing = *c.Racing
		}
		setNullable(&t.RaceOpenedAt, c.RaceOpenedAt)
		t.Version++
		t.UpdatedBy, t.UpdatedAt = c.UpdatedBy, c.UpdatedAt
		return st.put(t), nil
	})
}

func (s *Store) SetOrderKey(ctx context.Context, projectID, taskID string, expectedVersion int64, orderKey, columnID, updatedBy string, updatedAt int64) (store.Task, error) {
	if orderKey == "" {
		return store.Task{}, domain.Validation("an empty order key", nil)
	}
	if err := validWriter(updatedBy, updatedAt); err != nil {
		return store.Task{}, err
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.taskAt(projectID, taskID, expectedVersion)
		if err != nil {
			return store.Task{}, err
		}
		if columnID != "" {
			if err := st.columnOn(projectID, t.BoardID, columnID); err != nil {
				return store.Task{}, err
			}
			t.ColumnID = columnID
		}
		t.OrderKey = orderKey
		t.Version++
		t.UpdatedBy, t.UpdatedAt = updatedBy, updatedAt
		return st.put(t), nil
	})
}

func validFilter(f store.TaskFilter) error {
	if f.Assignee != nil && f.Unassigned {
		return domain.Validation("a filter takes an assignee or unassigned, not both", nil)
	}
	return nil
}

func (f taskMatch) matches(t store.Task) bool {
	switch {
	case t.ProjectID != f.project || t.BoardID != f.BoardID:
		return false
	case f.Status != nil && t.Status != *f.Status:
		return false
	case f.ColumnID != "" && t.ColumnID != f.ColumnID:
		return false
	case f.Assignee != nil && (t.Assignee == nil || *t.Assignee != *f.Assignee):
		return false
	case f.Unassigned && t.Assignee != nil:
		return false
	case f.Label != "" && !slices.Contains(t.Labels, f.Label):
		return false
	case f.RunOf != nil && (t.RunOf == nil || *t.RunOf != *f.RunOf):
		return false
	}
	return true
}

type taskMatch struct {
	project string
	store.TaskFilter
}

// matching is the board's tasks matching f in (order key, id) order, after the board is found.
func (st *state) matching(projectID string, f store.TaskFilter) ([]store.Task, error) {
	if err := validFilter(f); err != nil {
		return nil, err
	}
	if _, err := st.boardOf(projectID, f.BoardID); err != nil {
		return nil, err
	}
	m := taskMatch{projectID, f}
	var out []store.Task
	for _, t := range st.tasks {
		if m.matches(t) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, byOrderKey)
	return out, nil
}

func byOrderKey(a, b store.Task) int {
	return cmp.Or(strings.Compare(a.OrderKey, b.OrderKey), strings.Compare(a.ID, b.ID))
}

func (s *Store) ListTasks(ctx context.Context, projectID string, f store.TaskFilter, cursor string, limit int) (store.TaskPage, error) {
	var afterOrder, afterID string
	if cursor != "" {
		var err error
		if afterOrder, afterID, err = store.DecodeCursor(cursor); err != nil {
			return store.TaskPage{}, err
		}
	}
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) (store.TaskPage, error) {
		all, err := st.matching(projectID, f)
		if err != nil {
			return store.TaskPage{}, err
		}
		page := store.TaskPage{Tasks: []store.Task{}}
		for _, t := range all {
			if cursor != "" && byOrderKey(t, store.Task{OrderKey: afterOrder, ID: afterID}) <= 0 {
				continue
			}
			if len(page.Tasks) == limit {
				last := page.Tasks[limit-1]
				page.NextCursor = store.EncodeCursor(last.OrderKey, last.ID)
				break
			}
			page.Tasks = append(page.Tasks, cloneTask(t))
		}
		return page, nil
	})
}

func (s *Store) CountTasks(ctx context.Context, projectID string, f store.TaskFilter) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		all, err := st.matching(projectID, f)
		return int64(len(all)), err
	})
}

func (s *Store) LastOrderKey(ctx context.Context, projectID, boardID, columnID string) (string, error) {
	return get(ctx, s, func(st *state) (string, error) {
		var last *store.Task
		for _, t := range st.tasks {
			if t.ProjectID == projectID && t.BoardID == boardID && t.ColumnID == columnID && (last == nil || byOrderKey(t, *last) > 0) {
				last = &t
			}
		}
		if last == nil {
			return "", nil
		}
		return last.OrderKey, nil
	})
}

func (s *Store) ListTasksByIDs(ctx context.Context, projectID string, ids []string) ([]store.Task, error) {
	if len(ids) > 200 {
		return nil, domain.Validation("at most 200 ids", map[string]any{"ids": len(ids)})
	}
	return get(ctx, s, func(st *state) ([]store.Task, error) {
		out := []store.Task{}
		for _, id := range ids {
			if t, ok := st.tasks[[2]string{projectID, id}]; ok {
				out = append(out, cloneTask(t))
			}
		}
		return out, nil
	})
}

func (s *Store) DeleteTask(ctx context.Context, projectID, taskID string, expectedVersion int64) error {
	return s.do(ctx, func(st *state) error {
		if _, err := st.taskAt(projectID, taskID, expectedVersion); err != nil {
			return err
		}
		delete(st.tasks, [2]string{projectID, taskID})
		// The rows the schema cascades from the task that this store holds: its events, context
		// items, artifacts, comments, follow-ups, summary, edges, attachments and run row. Memory is not one of them (migration 0003): its task scopes are
		// the service's cascade (DeleteMemoryByTask), and a project entry keeps the task as provenance.
		for k, e := range st.events {
			if k[0] == projectID && e.TaskID != nil && *e.TaskID == taskID {
				delete(st.events, k)
			}
		}
		maps.DeleteFunc(st.contextItems, func(k [3]string, _ store.ContextItem) bool { return k[0] == projectID && k[1] == taskID })
		maps.DeleteFunc(st.artifacts, func(k [3]string, _ store.Artifact) bool { return k[0] == projectID && k[1] == taskID })
		maps.DeleteFunc(st.comments, func(k [3]string, _ store.Comment) bool { return k[0] == projectID && k[1] == taskID })
		maps.DeleteFunc(st.followUps, func(k [3]string, _ store.FollowUp) bool { return k[0] == projectID && k[1] == taskID })
		delete(st.summaries, [2]string{projectID, taskID})
		maps.DeleteFunc(st.edges, func(k [2]string, e store.DependencyEdge) bool {
			return k[0] == projectID && (e.FromTaskID == taskID || e.ToTaskID == taskID)
		})
		maps.DeleteFunc(st.attachments, func(k [3]string, _ store.Attachment) bool { return k[0] == projectID && k[1] == taskID })
		delete(st.runs, [2]string{projectID, taskID})
		return nil
	})
}
