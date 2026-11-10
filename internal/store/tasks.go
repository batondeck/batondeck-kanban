package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The tasks area of contract_tasks.go, on both engines. A versioned write reads the task inside
// Atomically (locked to the transaction on Postgres; SQLite's transaction holds the write lock from
// BEGIN), judges its version and the change in Go, and writes one UPDATE.

type taskQueries interface {
	GetTask(ctx context.Context, arg sqlitedb.GetTaskParams) (sqlitedb.GetTaskRow, error)
	LockTask(ctx context.Context, arg sqlitedb.LockTaskParams) (sqlitedb.LockTaskRow, error)
	InsertTask(ctx context.Context, arg sqlitedb.InsertTaskParams) (sqlitedb.InsertTaskRow, error)
	WriteTaskFields(ctx context.Context, arg sqlitedb.WriteTaskFieldsParams) (sqlitedb.WriteTaskFieldsRow, error)
	WriteTaskState(ctx context.Context, arg sqlitedb.WriteTaskStateParams) (sqlitedb.WriteTaskStateRow, error)
	ListTasks(ctx context.Context, arg sqlitedb.ListTasksParams) ([]sqlitedb.ListTasksRow, error)
	CountTasks(ctx context.Context, arg sqlitedb.CountTasksParams) (sqlitedb.CountTasksRow, error)
	ListTasksByIDs(ctx context.Context, arg sqlitedb.ListTasksByIDsParams) ([]sqlitedb.ListTasksByIDsRow, error)
	DeleteTask(ctx context.Context, arg sqlitedb.DeleteTaskParams) (int64, error)
	GetColumn(ctx context.Context, arg sqlitedb.GetColumnParams) (sqlitedb.Column, error)
}

func (s *SQLite) CreateTask(ctx context.Context, in CreateTaskInput) (Task, error) {
	return createTask(ctx, s, in)
}

func (s *Postgres) CreateTask(ctx context.Context, in CreateTaskInput) (Task, error) {
	return createTask(ctx, s, in)
}

func (s *SQLite) GetTask(ctx context.Context, projectID, taskID string) (Task, error) {
	return getTask(ctx, s, projectID, taskID)
}

func (s *Postgres) GetTask(ctx context.Context, projectID, taskID string) (Task, error) {
	return getTask(ctx, s, projectID, taskID)
}

func (s *SQLite) LockTask(ctx context.Context, projectID, taskID string) (Task, error) {
	return lockTask(ctx, s, projectID, taskID)
}

func (s *Postgres) LockTask(ctx context.Context, projectID, taskID string) (Task, error) {
	return lockTask(ctx, s, projectID, taskID)
}

func (s *SQLite) UpdateTask(ctx context.Context, projectID, taskID string, expectedVersion int64, patch TaskPatch) (Task, error) {
	return updateTask(ctx, s, projectID, taskID, expectedVersion, patch)
}

func (s *Postgres) UpdateTask(ctx context.Context, projectID, taskID string, expectedVersion int64, patch TaskPatch) (Task, error) {
	return updateTask(ctx, s, projectID, taskID, expectedVersion, patch)
}

func (s *SQLite) SetStatus(ctx context.Context, projectID, taskID string, expectedVersion int64, change StatusChange) (Task, error) {
	return setStatus(ctx, s, projectID, taskID, expectedVersion, change)
}

func (s *Postgres) SetStatus(ctx context.Context, projectID, taskID string, expectedVersion int64, change StatusChange) (Task, error) {
	return setStatus(ctx, s, projectID, taskID, expectedVersion, change)
}

func (s *SQLite) SetOrderKey(ctx context.Context, projectID, taskID string, expectedVersion int64, orderKey, columnID, updatedBy string, updatedAt int64) (Task, error) {
	return setOrderKey(ctx, s, projectID, taskID, expectedVersion, orderKey, columnID, updatedBy, updatedAt)
}

func (s *Postgres) SetOrderKey(ctx context.Context, projectID, taskID string, expectedVersion int64, orderKey, columnID, updatedBy string, updatedAt int64) (Task, error) {
	return setOrderKey(ctx, s, projectID, taskID, expectedVersion, orderKey, columnID, updatedBy, updatedAt)
}

func (s *SQLite) ListTasks(ctx context.Context, projectID string, f TaskFilter, cursor string, limit int) (TaskPage, error) {
	return listTasks(ctx, s, projectID, f, cursor, limit)
}

func (s *Postgres) ListTasks(ctx context.Context, projectID string, f TaskFilter, cursor string, limit int) (TaskPage, error) {
	return listTasks(ctx, s, projectID, f, cursor, limit)
}

func (s *SQLite) CountTasks(ctx context.Context, projectID string, f TaskFilter) (int64, error) {
	return countTasks(ctx, s, projectID, f)
}

func (s *Postgres) CountTasks(ctx context.Context, projectID string, f TaskFilter) (int64, error) {
	return countTasks(ctx, s, projectID, f)
}

func (s *SQLite) ListTasksByIDs(ctx context.Context, projectID string, ids []string) ([]Task, error) {
	return listTasksByIDs(ctx, s, projectID, ids)
}

func (s *Postgres) ListTasksByIDs(ctx context.Context, projectID string, ids []string) ([]Task, error) {
	return listTasksByIDs(ctx, s, projectID, ids)
}

func (s *SQLite) DeleteTask(ctx context.Context, projectID, taskID string, expectedVersion int64) error {
	return deleteTask(ctx, s, projectID, taskID, expectedVersion)
}

func (s *Postgres) DeleteTask(ctx context.Context, projectID, taskID string, expectedVersion int64) error {
	return deleteTask(ctx, s, projectID, taskID, expectedVersion)
}

// --- validation: the same refusals, in the same order, as memstore's ---

var priorityRank = map[domain.Priority]int{
	domain.PriorityLow: 0, domain.PriorityNormal: 1, domain.PriorityHigh: 2, domain.PriorityUrgent: 3,
}

func validPriority(p domain.Priority) error {
	if _, ok := priorityRank[p]; !ok {
		return domain.Validation("unknown priority: "+string(p), map[string]any{"priority": p})
	}
	return nil
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

func validWriter(by string, at int64) error {
	if by == "" || at <= 0 {
		return domain.Validation("a write needs who made it and when", nil)
	}
	return nil
}

func validCreate(in CreateTaskInput) error {
	switch {
	case strings.TrimSpace(in.Title) == "":
		return domain.Validation("a task needs a title", nil)
	case in.CreatedBy == "":
		return domain.Validation("a task needs a creator", nil)
	case in.OrderKey == "":
		return domain.Validation("a task needs an order key", nil)
	case in.CreatedAt <= 0:
		return domain.Validation("a task needs its creation time", nil)
	}
	if _, err := domain.ParseStatus(string(in.Status)); err != nil {
		return err
	}
	if in.Priority != "" {
		if err := validPriority(in.Priority); err != nil {
			return err
		}
	}
	if err := validCustomFields(in.CustomFields); err != nil {
		return err
	}
	return validJSON("modelHint", in.ModelHint)
}

func validPatch(p TaskPatch) error {
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
	for _, f := range []struct {
		name string
		n    *Nullable[json.RawMessage]
	}{{"review", p.Review}, {"chars", p.Chars}, {"modelHint", p.ModelHint}} {
		if f.n != nil && f.n.Value != nil {
			if err := validJSON(f.name, *f.n.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validFilter(f TaskFilter) error {
	if f.Assignee != nil && f.Unassigned {
		return domain.Validation("a filter takes an assignee or unassigned, not both", nil)
	}
	return nil
}

// clamp is a list limit as the contracts read one: <= 0 is def, above max is max.
func clamp(limit, def, max int) int {
	switch {
	case limit <= 0:
		return def
	case limit > max:
		return max
	}
	return limit
}

// --- the operations ---

func createTask(ctx context.Context, e engine, in CreateTaskInput) (Task, error) {
	if err := validCreate(in); err != nil {
		return Task{}, err
	}
	priority := in.Priority
	if priority == "" {
		priority = domain.PriorityNormal
	}
	custom := in.CustomFields
	if custom == nil {
		custom = json.RawMessage(`{}`)
	}
	var out Task
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		if err := placeOf(ctx, q, in.ProjectID, in.BoardID, in.ColumnID); err != nil {
			return err
		}
		n, err := q.NextTaskNumber(ctx, in.ProjectID)
		if err != nil {
			return fmt.Errorf("store: task counter: %w", err)
		}
		row, err := q.InsertTask(ctx, sqlitedb.InsertTaskParams{
			ProjectID: in.ProjectID, ID: fmt.Sprintf("T-%d", n), BoardID: in.BoardID, ColumnID: in.ColumnID,
			Title: in.Title, Description: in.Description, Status: string(in.Status), Priority: string(priority),
			Assignee: nullStr(in.Assignee), Labels: jsonList(in.Labels), RequiredCapabilities: jsonList(in.RequiredCapabilities),
			ParentTaskID: nullStr(in.ParentTaskID), OrderKey: in.OrderKey, CustomFields: string(custom),
			ModelHint: nullRaw(in.ModelHint), RunOf: nullStr(in.RunOf), SprintIds: jsonList(in.SprintIDs),
			CreatedBy: in.CreatedBy, CreatedByAgent: nullStr(in.CreatedByAgent), CreatedAt: in.CreatedAt,
			UpdatedBy: in.CreatedBy, UpdatedAt: in.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("store: create task: %w", err)
		}
		out, err = taskFromRow(sqlitedb.GetTaskRow(row))
		return err
	})
	return out, err
}

// placeOf refuses a column that is not the board's, or a board that is not the project's, naming
// the first that is missing: the project, the board, the column. The column found is one statement.
func placeOf(ctx context.Context, q sqlQueries, projectID, boardID, columnID string) error {
	_, err := q.GetColumn(ctx, sqlitedb.GetColumnParams{ProjectID: projectID, BoardID: boardID, ID: columnID})
	if err == nil {
		return nil
	}
	if !isNoRows(err) {
		return fmt.Errorf("store: column: %w", err)
	}
	if _, err := q.GetProject(ctx, projectID); err != nil {
		return found(err, "Project", projectID)
	}
	if _, err := q.GetBoard(ctx, sqlitedb.GetBoardParams{ProjectID: projectID, ID: boardID}); err != nil {
		return found(err, "Board", boardID)
	}
	return domain.NotFound("Column", columnID)
}

// columnOn refuses a column id that is not one of the board's.
func columnOn(ctx context.Context, q sqlQueries, projectID, boardID, columnID string) error {
	_, err := q.GetColumn(ctx, sqlitedb.GetColumnParams{ProjectID: projectID, BoardID: boardID, ID: columnID})
	return found(err, "Column", columnID)
}

func getTask(ctx context.Context, e engine, projectID, taskID string) (Task, error) {
	row, err := e.queries(false).GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: projectID, ID: taskID})
	if err := found(err, "Task", taskID); err != nil {
		return Task{}, err
	}
	return taskFromRow(row)
}

func lockTask(ctx context.Context, e engine, projectID, taskID string) (Task, error) {
	return lockedTask(ctx, e.queries(true), projectID, taskID)
}

func lockedTask(ctx context.Context, q sqlQueries, projectID, taskID string) (Task, error) {
	row, err := q.LockTask(ctx, sqlitedb.LockTaskParams{ProjectID: projectID, ID: taskID})
	if err := found(err, "Task", taskID); err != nil {
		return Task{}, err
	}
	return taskFromRow(sqlitedb.GetTaskRow(row))
}

// taskAt is the task at expectedVersion, read for a write inside the transaction: NotFound when
// there is none, Stale when its version is another.
func taskAt(ctx context.Context, q sqlQueries, projectID, taskID string, expectedVersion int64) (Task, error) {
	t, err := lockedTask(ctx, q, projectID, taskID)
	if err != nil {
		return Task{}, err
	}
	if t.Version != expectedVersion {
		return Task{}, domain.Stale(expectedVersion, t.Version)
	}
	return t, nil
}

// versioned runs change on the task at expectedVersion inside one transaction and returns the task
// change wrote.
func versioned(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64, change func(q sqlQueries, t Task) (Task, error)) (Task, error) {
	var out Task
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		t, err := taskAt(ctx, q, projectID, taskID, expectedVersion)
		if err != nil {
			return err
		}
		out, err = change(q, t)
		return err
	})
	return out, err
}

func setNullable[T any](dst **T, n *Nullable[T]) {
	if n != nil {
		*dst = n.Value
	}
}

func setNullableJSON(dst *json.RawMessage, n *Nullable[json.RawMessage]) {
	if n == nil {
		return
	}
	if n.Value == nil {
		*dst = nil
		return
	}
	*dst = *n.Value
}

func updateTask(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64, p TaskPatch) (Task, error) {
	if err := validPatch(p); err != nil {
		return Task{}, err
	}
	return versioned(ctx, e, projectID, taskID, expectedVersion, func(q sqlQueries, t Task) (Task, error) {
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
			t.Labels = *p.Labels
		}
		if p.RequiredCapabilities != nil {
			t.RequiredCapabilities = *p.RequiredCapabilities
		}
		setNullable(&t.Estimate, p.Estimate)
		setNullable(&t.DueAt, p.DueAt)
		if p.CustomFields != nil {
			t.CustomFields = *p.CustomFields
		}
		setNullable(&t.Deliverable, p.Deliverable)
		setNullable(&t.Reviewer, p.Reviewer)
		setNullableJSON(&t.Review, p.Review)
		setNullableJSON(&t.Chars, p.Chars)
		setNullableJSON(&t.ModelHint, p.ModelHint)
		if p.SprintIDs != nil {
			t.SprintIDs = *p.SprintIDs
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
		row, err := q.WriteTaskFields(ctx, sqlitedb.WriteTaskFieldsParams{
			Title: t.Title, Description: t.Description, Priority: string(t.Priority), Assignee: nullStr(t.Assignee),
			Labels: jsonList(t.Labels), RequiredCapabilities: jsonList(t.RequiredCapabilities),
			Estimate: nullFloat(t.Estimate), DueAt: nullInt(t.DueAt), CustomFields: string(t.CustomFields),
			Deliverable: nullStr(t.Deliverable), Reviewer: nullStr(t.Reviewer), Review: nullRaw(t.Review),
			Chars: nullRaw(t.Chars), ModelHint: nullRaw(t.ModelHint), SprintIds: jsonList(t.SprintIDs),
			Racing: boolInt(t.Racing), RaceOpenedAt: nullInt(t.RaceOpenedAt), Score: t.Score, SearchTokens: t.SearchTokens,
			Version: t.Version + 1, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt, ProjectID: projectID, ID: taskID,
		})
		if err != nil {
			return Task{}, fmt.Errorf("store: update task: %w", err)
		}
		return taskFromRow(sqlitedb.GetTaskRow(row))
	})
}

// writeState writes t's status, place, retry state and lease, at its version one higher.
func writeState(ctx context.Context, q sqlQueries, t Task, updatedBy string, updatedAt int64) (Task, error) {
	p := sqlitedb.WriteTaskStateParams{
		Status: string(t.Status), ColumnID: t.ColumnID, OrderKey: t.OrderKey, BlockedReason: nullStr(t.BlockedReason),
		Labels: jsonList(t.Labels), ClaimAttempts: t.ClaimAttempts, NextEligibleAt: nullInt(t.NextEligibleAt),
		Assignee: nullStr(t.Assignee), Deliverable: nullStr(t.Deliverable), Review: nullRaw(t.Review),
		Racing: boolInt(t.Racing), RaceOpenedAt: nullInt(t.RaceOpenedAt), Version: t.Version + 1, UpdatedBy: updatedBy, UpdatedAt: updatedAt, ProjectID: t.ProjectID, ID: t.ID,
	}
	if c := t.Claim; c != nil {
		p.ClaimOwner, p.ClaimAgent, p.ClaimAgentID = sql.NullString{String: c.Owner, Valid: true}, nullStr(c.Agent), nullStr(c.AgentID)
		p.LeaseID, p.LeaseExpiresAt = sql.NullString{String: c.LeaseID, Valid: true}, sql.NullInt64{Int64: c.ExpiresAt, Valid: true}
	}
	row, err := q.WriteTaskState(ctx, p)
	if err != nil {
		return Task{}, fmt.Errorf("store: task state: %w", err)
	}
	return taskFromRow(sqlitedb.GetTaskRow(row))
}

func setStatus(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64, c StatusChange) (Task, error) {
	if _, err := domain.ParseStatus(string(c.Status)); err != nil {
		return Task{}, err
	}
	if err := validWriter(c.UpdatedBy, c.UpdatedAt); err != nil {
		return Task{}, err
	}
	if c.OrderKey != nil && *c.OrderKey == "" {
		return Task{}, domain.Validation("an empty order key", nil)
	}
	if c.Review != nil && c.Review.Value != nil {
		if err := validJSON("review", *c.Review.Value); err != nil {
			return Task{}, err
		}
	}
	return versioned(ctx, e, projectID, taskID, expectedVersion, func(q sqlQueries, t Task) (Task, error) {
		reopened := t.Status == domain.Done && c.Status != domain.Done
		if c.ColumnID != "" {
			if err := columnOn(ctx, q, projectID, t.BoardID, c.ColumnID); err != nil {
				return Task{}, err
			}
			t.ColumnID = c.ColumnID
		}
		if c.Unpoison {
			reason := ""
			if t.BlockedReason != nil {
				reason = *t.BlockedReason
			}
			if !domain.IsPoisoned(t.Status, reason) {
				return Task{}, domain.Validation("Unpoison on a task that is not poisoned", map[string]any{"taskId": taskID})
			}
			out := domain.Unpoison(t.Labels)
			t.Labels = out.Labels
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
			t.Labels = c.Labels
		}
		if reopened {
			t.Labels = domain.Reopened(t.Labels)
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
		return writeState(ctx, q, t, c.UpdatedBy, c.UpdatedAt)
	})
}

func setOrderKey(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64, orderKey, columnID, updatedBy string, updatedAt int64) (Task, error) {
	if orderKey == "" {
		return Task{}, domain.Validation("an empty order key", nil)
	}
	if err := validWriter(updatedBy, updatedAt); err != nil {
		return Task{}, err
	}
	return versioned(ctx, e, projectID, taskID, expectedVersion, func(q sqlQueries, t Task) (Task, error) {
		if columnID != "" {
			if err := columnOn(ctx, q, projectID, t.BoardID, columnID); err != nil {
				return Task{}, err
			}
			t.ColumnID = columnID
		}
		t.OrderKey = orderKey
		return writeState(ctx, q, t, updatedBy, updatedAt)
	})
}

// filterParams is f as the list and count statements read it.
type filterParams struct {
	status       sql.NullString
	assigneeMode int64
	assignee     sql.NullString
	runOf        sql.NullString
}

func filterOf(f TaskFilter) filterParams {
	p := filterParams{runOf: nullStr(f.RunOf)}
	if f.Status != nil {
		p.status = sql.NullString{String: string(*f.Status), Valid: true}
	}
	switch {
	case f.Assignee != nil:
		p.assigneeMode, p.assignee = 1, sql.NullString{String: *f.Assignee, Valid: true}
	case f.Unassigned:
		p.assigneeMode = 2
	}
	return p
}

// listTasks is one statement for a page holding a task, two for an empty one: an empty page asks
// whether the board is the project's.
func listTasks(ctx context.Context, e engine, projectID string, f TaskFilter, cursor string, limit int) (TaskPage, error) {
	if err := validFilter(f); err != nil {
		return TaskPage{}, err
	}
	var afterOrder, afterID string
	if cursor != "" {
		var err error
		if afterOrder, afterID, err = DecodeCursor(cursor); err != nil {
			return TaskPage{}, err
		}
	}
	limit = clamp(limit, 50, 200)
	fp := filterOf(f)
	q := e.queries(false)
	rows, err := q.ListTasks(ctx, sqlitedb.ListTasksParams{
		ProjectID: projectID, BoardID: f.BoardID, Status: fp.status, ColumnID: f.ColumnID, AssigneeMode: fp.assigneeMode,
		Assignee: fp.assignee, Label: f.Label, RunOf: fp.runOf, AfterID: afterID, AfterOrder: afterOrder, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return TaskPage{}, fmt.Errorf("store: list tasks: %w", err)
	}
	if len(rows) == 0 {
		if _, err := q.GetBoard(ctx, sqlitedb.GetBoardParams{ProjectID: projectID, ID: f.BoardID}); err != nil {
			return TaskPage{}, found(err, "Board", f.BoardID)
		}
		return TaskPage{Tasks: []Task{}}, nil
	}
	page := TaskPage{Tasks: make([]Task, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := page.Tasks[limit-1]
			page.NextCursor = EncodeCursor(last.OrderKey, last.ID)
			break
		}
		t, err := taskFromRow(sqlitedb.GetTaskRow(r))
		if err != nil {
			return TaskPage{}, err
		}
		page.Tasks = append(page.Tasks, t)
	}
	return page, nil
}

func countTasks(ctx context.Context, e engine, projectID string, f TaskFilter) (int64, error) {
	if err := validFilter(f); err != nil {
		return 0, err
	}
	fp := filterOf(f)
	r, err := e.queries(false).CountTasks(ctx, sqlitedb.CountTasksParams{
		ProjectID: projectID, BoardID: f.BoardID, Status: fp.status, ColumnID: f.ColumnID, AssigneeMode: fp.assigneeMode,
		Assignee: fp.assignee, Label: f.Label, RunOf: fp.runOf,
	})
	if err != nil {
		return 0, fmt.Errorf("store: count tasks: %w", err)
	}
	if r.BoardFound == 0 {
		return 0, domain.NotFound("Board", f.BoardID)
	}
	return r.TaskCount, nil
}

func listTasksByIDs(ctx context.Context, e engine, projectID string, ids []string) ([]Task, error) {
	if len(ids) > 200 {
		return nil, domain.Validation("at most 200 ids", map[string]any{"ids": len(ids)})
	}
	out := []Task{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := e.queries(false).ListTasksByIDs(ctx, sqlitedb.ListTasksByIDsParams{ProjectID: projectID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("store: tasks by id: %w", err)
	}
	byID := make(map[string]Task, len(rows))
	for _, r := range rows {
		t, err := taskFromRow(sqlitedb.GetTaskRow(r))
		if err != nil {
			return nil, err
		}
		byID[t.ID] = t
	}
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// deleteTask is one DELETE at the version; when it deletes nothing, one read names the refusal.
func deleteTask(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64) error {
	q := e.queries(true)
	n, err := q.DeleteTask(ctx, sqlitedb.DeleteTaskParams{ProjectID: projectID, ID: taskID, Version: expectedVersion})
	if err != nil {
		return fmt.Errorf("store: delete task: %w", err)
	}
	if n == 1 {
		return nil
	}
	row, err := q.GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: projectID, ID: taskID})
	if err := found(err, "Task", taskID); err != nil {
		return err
	}
	return domain.Stale(expectedVersion, row.Version)
}

// --- rows ---

// taskFromRow maps the one task row every task query selects.
func taskFromRow(r sqlitedb.GetTaskRow) (Task, error) {
	t := Task{
		ProjectID: r.ProjectID, ID: r.ID, BoardID: r.BoardID, ColumnID: r.ColumnID, Title: r.Title,
		Description: r.Description, Status: domain.Status(r.Status), BlockedReason: strPtr(r.BlockedReason),
		Priority: domain.Priority(r.Priority), Assignee: strPtr(r.Assignee), Estimate: floatPtr(r.Estimate),
		DueAt: intPtr(r.DueAt), ParentTaskID: strPtr(r.ParentTaskID), OrderKey: r.OrderKey,
		CustomFields: json.RawMessage(r.CustomFields), Deliverable: strPtr(r.Deliverable), Reviewer: strPtr(r.Reviewer),
		Review: rawOrNil(r.Review), Chars: rawOrNil(r.Chars), ModelHint: rawOrNil(r.ModelHint), RunOf: strPtr(r.RunOf),
		Racing: r.Racing != 0, RaceOpenedAt: intPtr(r.RaceOpenedAt), ClaimAttempts: r.ClaimAttempts,
		NextEligibleAt: intPtr(r.NextEligibleAt), Score: r.Score, SearchTokens: r.SearchTokens, Version: r.Version,
		CreatedBy: r.CreatedBy, CreatedByAgent: strPtr(r.CreatedByAgent), CreatedAt: r.CreatedAt,
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt,
	}
	for _, l := range []struct {
		dst *[]string
		src string
	}{{&t.Labels, r.Labels}, {&t.RequiredCapabilities, r.RequiredCapabilities}, {&t.BlockedBy, r.BlockedBy}, {&t.Blocks, r.Blocks}, {&t.SprintIDs, r.SprintIds}} {
		v, err := readList(l.src)
		if err != nil {
			return Task{}, fmt.Errorf("store: task %s/%s: %w", r.ProjectID, r.ID, err)
		}
		*l.dst = v
	}
	if r.ClaimOwner.Valid {
		t.Claim = &TaskClaim{
			Owner: r.ClaimOwner.String, Agent: strPtr(r.ClaimAgent), AgentID: strPtr(r.ClaimAgentID),
			LeaseID: r.LeaseID.String, ExpiresAt: r.LeaseExpiresAt.Int64,
		}
	}
	return t, nil
}

// jsonList is a list column's value: nil is [].
func jsonList(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

// readList reads a list column back, never nil.
func readList(s string) ([]string, error) {
	out := []string{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func nullStr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

// nullRaw is a nullable JSON column's value, kept byte for byte: nil is NULL.
func nullRaw(r json.RawMessage) sql.NullString {
	if r == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(r), Valid: true}
}

func rawOrNil(n sql.NullString) json.RawMessage {
	if !n.Valid {
		return nil
	}
	return json.RawMessage(n.String)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) GetTask(ctx context.Context, arg sqlitedb.GetTaskParams) (sqlitedb.GetTaskRow, error) {
	r, err := p.q.GetTask(ctx, pgdb.GetTaskParams(arg))
	return sqlitedb.GetTaskRow(r), err
}

func (p pgQueries) LockTask(ctx context.Context, arg sqlitedb.LockTaskParams) (sqlitedb.LockTaskRow, error) {
	r, err := p.q.LockTask(ctx, pgdb.LockTaskParams(arg))
	return sqlitedb.LockTaskRow(r), err
}

func (p pgQueries) InsertTask(ctx context.Context, arg sqlitedb.InsertTaskParams) (sqlitedb.InsertTaskRow, error) {
	r, err := p.q.InsertTask(ctx, pgdb.InsertTaskParams(arg))
	return sqlitedb.InsertTaskRow(r), err
}

func (p pgQueries) WriteTaskFields(ctx context.Context, arg sqlitedb.WriteTaskFieldsParams) (sqlitedb.WriteTaskFieldsRow, error) {
	r, err := p.q.WriteTaskFields(ctx, pgdb.WriteTaskFieldsParams(arg))
	return sqlitedb.WriteTaskFieldsRow(r), err
}

func (p pgQueries) WriteTaskState(ctx context.Context, arg sqlitedb.WriteTaskStateParams) (sqlitedb.WriteTaskStateRow, error) {
	r, err := p.q.WriteTaskState(ctx, pgdb.WriteTaskStateParams(arg))
	return sqlitedb.WriteTaskStateRow(r), err
}

func (p pgQueries) ListTasks(ctx context.Context, arg sqlitedb.ListTasksParams) ([]sqlitedb.ListTasksRow, error) {
	rows, err := p.q.ListTasks(ctx, pgdb.ListTasksParams(arg))
	return convert(rows, func(r pgdb.ListTasksRow) sqlitedb.ListTasksRow { return sqlitedb.ListTasksRow(r) }), err
}

func (p pgQueries) CountTasks(ctx context.Context, arg sqlitedb.CountTasksParams) (sqlitedb.CountTasksRow, error) {
	r, err := p.q.CountTasks(ctx, pgdb.CountTasksParams(arg))
	return sqlitedb.CountTasksRow(r), err
}

func (p pgQueries) ListTasksByIDs(ctx context.Context, arg sqlitedb.ListTasksByIDsParams) ([]sqlitedb.ListTasksByIDsRow, error) {
	rows, err := p.q.ListTasksByIDs(ctx, pgdb.ListTasksByIDsParams(arg))
	return convert(rows, func(r pgdb.ListTasksByIDsRow) sqlitedb.ListTasksByIDsRow { return sqlitedb.ListTasksByIDsRow(r) }), err
}

func (p pgQueries) DeleteTask(ctx context.Context, arg sqlitedb.DeleteTaskParams) (int64, error) {
	return p.q.DeleteTask(ctx, pgdb.DeleteTaskParams(arg))
}

func (p pgQueries) GetColumn(ctx context.Context, arg sqlitedb.GetColumnParams) (sqlitedb.Column, error) {
	r, err := p.q.GetColumn(ctx, pgdb.GetColumnParams(arg))
	return sqlitedb.Column(r), err
}
