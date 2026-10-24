package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The follow-up area of contract_followups.go, on both engines. Each call is one statement: the
// insert and the page start FROM the task, so a missing task is their own empty answer.

type followUpQueries interface {
	InsertFollowUp(ctx context.Context, arg sqlitedb.InsertFollowUpParams) (sqlitedb.FollowUp, error)
	GetFollowUp(ctx context.Context, arg sqlitedb.GetFollowUpParams) (sqlitedb.FollowUp, error)
	ListFollowUpsOldest(ctx context.Context, arg sqlitedb.ListFollowUpsOldestParams) ([]sqlitedb.ListFollowUpsOldestRow, error)
	AckFollowUp(ctx context.Context, arg sqlitedb.AckFollowUpParams) (sqlitedb.FollowUp, error)
}

func (s *SQLite) AddFollowUp(ctx context.Context, in AddFollowUpInput) (FollowUp, error) {
	return addFollowUp(ctx, s, in)
}

func (s *Postgres) AddFollowUp(ctx context.Context, in AddFollowUpInput) (FollowUp, error) {
	return addFollowUp(ctx, s, in)
}

func (s *SQLite) GetFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error) {
	return getFollowUp(ctx, s, projectID, taskID, id)
}

func (s *Postgres) GetFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error) {
	return getFollowUp(ctx, s, projectID, taskID, id)
}

func (s *SQLite) ListFollowUps(ctx context.Context, projectID, taskID string, q FollowUpsQuery) (FollowUpsPage, error) {
	return listFollowUps(ctx, s, projectID, taskID, q)
}

func (s *Postgres) ListFollowUps(ctx context.Context, projectID, taskID string, q FollowUpsQuery) (FollowUpsPage, error) {
	return listFollowUps(ctx, s, projectID, taskID, q)
}

func (s *SQLite) AckFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error) {
	return ackFollowUp(ctx, s, projectID, taskID, id)
}

func (s *Postgres) AckFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error) {
	return ackFollowUp(ctx, s, projectID, taskID, id)
}

func followUpOf(r sqlitedb.FollowUp) FollowUp {
	return FollowUp{ProjectID: r.ProjectID, TaskID: r.TaskID, ID: r.ID, Body: r.Body, Author: r.Author, Target: strPtr(r.Target),
		TargetIdentity: strPtr(r.TargetIdentity), Status: FollowUpStatus(r.Status), Reopened: r.Reopened == 1, TS: r.Ts}
}

func addFollowUp(ctx context.Context, e engine, in AddFollowUpInput) (FollowUp, error) {
	switch {
	case in.Body == "":
		return FollowUp{}, domain.Validation("a follow-up needs a body", nil)
	case in.Author == "":
		return FollowUp{}, domain.Validation("a follow-up needs an author", nil)
	}
	var reopened int64
	if in.Reopened {
		reopened = 1
	}
	row, err := e.queries(true).InsertFollowUp(ctx, sqlitedb.InsertFollowUpParams{
		ID: newID("FU-", 16), Body: in.Body, Author: in.Author, Target: nullStr(in.Target), TargetIdentity: nullStr(in.TargetIdentity),
		Reopened: reopened, Ts: nowMillis(), ProjectID: in.ProjectID, TaskID: in.TaskID,
	})
	if err := found(err, "Task", in.TaskID); err != nil {
		return FollowUp{}, err
	}
	return followUpOf(row), nil
}

func getFollowUp(ctx context.Context, e engine, projectID, taskID, id string) (FollowUp, error) {
	row, err := e.queries(false).GetFollowUp(ctx, sqlitedb.GetFollowUpParams{ProjectID: projectID, TaskID: taskID, ID: id})
	if err := found(err, "FollowUp", id); err != nil {
		return FollowUp{}, err
	}
	return followUpOf(row), nil
}

func listFollowUps(ctx context.Context, e engine, projectID, taskID string, q FollowUpsQuery) (FollowUpsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return FollowUpsPage{}, err
	}
	limit := clamp(q.Limit, 100, 200)
	var openOnly int64
	if q.OpenOnly {
		openOnly = 1
	}
	rows, err := e.queries(false).ListFollowUpsOldest(ctx, sqlitedb.ListFollowUpsOldestParams{
		OpenOnly: openOnly, AfterID: afterID, AfterTs: afterTS, ProjectID: projectID, TaskID: taskID, PageLimit: int64(limit + 1)})
	if err != nil {
		return FollowUpsPage{}, fmt.Errorf("store: list follow-ups: %w", err)
	}
	if len(rows) == 0 {
		return FollowUpsPage{}, domain.NotFound("Task", taskID)
	}
	page := FollowUpsPage{Items: []FollowUp{}}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
			break
		}
		page.Items = append(page.Items, followUpOf(sqlitedb.FollowUp{ProjectID: projectID, TaskID: taskID, ID: r.ID.String,
			Body: r.Body.String, Author: r.Author.String, Target: r.Target, TargetIdentity: r.TargetIdentity, Status: r.Status.String,
			Reopened: r.Reopened.Int64, Ts: r.Ts.Int64}))
	}
	return page, nil
}

func ackFollowUp(ctx context.Context, e engine, projectID, taskID, id string) (FollowUp, error) {
	row, err := e.queries(true).AckFollowUp(ctx, sqlitedb.AckFollowUpParams{ProjectID: projectID, TaskID: taskID, ID: id})
	if err := found(err, "FollowUp", id); err != nil {
		return FollowUp{}, err
	}
	return followUpOf(row), nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertFollowUp(ctx context.Context, arg sqlitedb.InsertFollowUpParams) (sqlitedb.FollowUp, error) {
	r, err := p.q.InsertFollowUp(ctx, pgdb.InsertFollowUpParams(arg))
	return sqlitedb.FollowUp(r), err
}

func (p pgQueries) GetFollowUp(ctx context.Context, arg sqlitedb.GetFollowUpParams) (sqlitedb.FollowUp, error) {
	r, err := p.q.GetFollowUp(ctx, pgdb.GetFollowUpParams(arg))
	return sqlitedb.FollowUp(r), err
}

func (p pgQueries) ListFollowUpsOldest(ctx context.Context, arg sqlitedb.ListFollowUpsOldestParams) ([]sqlitedb.ListFollowUpsOldestRow, error) {
	rows, err := p.q.ListFollowUpsOldest(ctx, pgdb.ListFollowUpsOldestParams(arg))
	return convert(rows, func(r pgdb.ListFollowUpsOldestRow) sqlitedb.ListFollowUpsOldestRow {
		return sqlitedb.ListFollowUpsOldestRow(r)
	}), err
}

func (p pgQueries) AckFollowUp(ctx context.Context, arg sqlitedb.AckFollowUpParams) (sqlitedb.FollowUp, error) {
	r, err := p.q.AckFollowUp(ctx, pgdb.AckFollowUpParams(arg))
	return sqlitedb.FollowUp(r), err
}
