package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The context-item area of contract_context.go, on both engines. Each call is one statement: the
// insert and the page both start FROM the task, so a missing task is their own empty answer.

type contextQueries interface {
	InsertContextItem(ctx context.Context, arg sqlitedb.InsertContextItemParams) (sqlitedb.ContextItem, error)
	ListContextItemsNewest(ctx context.Context, arg sqlitedb.ListContextItemsNewestParams) ([]sqlitedb.ListContextItemsNewestRow, error)
	ListContextItemsOldest(ctx context.Context, arg sqlitedb.ListContextItemsOldestParams) ([]sqlitedb.ListContextItemsOldestRow, error)
	DeleteContextItemsByTask(ctx context.Context, arg sqlitedb.DeleteContextItemsByTaskParams) (int64, error)
}

func (s *SQLite) AddContextItem(ctx context.Context, in AddContextItemInput) (ContextItem, error) {
	return addContextItem(ctx, s, in)
}

func (s *Postgres) AddContextItem(ctx context.Context, in AddContextItemInput) (ContextItem, error) {
	return addContextItem(ctx, s, in)
}

func (s *SQLite) ListContextItems(ctx context.Context, projectID, taskID string, q ContextItemsQuery) (ContextItemsPage, error) {
	return listContextItems(ctx, s, projectID, taskID, q)
}

func (s *Postgres) ListContextItems(ctx context.Context, projectID, taskID string, q ContextItemsQuery) (ContextItemsPage, error) {
	return listContextItems(ctx, s, projectID, taskID, q)
}

func (s *SQLite) DeleteContextItemsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteContextItemsByTask(ctx, s, projectID, taskID)
}

func (s *Postgres) DeleteContextItemsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteContextItemsByTask(ctx, s, projectID, taskID)
}

var contextKinds = map[ContextKind]bool{ContextNote: true, ContextDecision: true, ContextField: true, ContextLink: true}

func addContextItem(ctx context.Context, e engine, in AddContextItemInput) (ContextItem, error) {
	switch {
	case !contextKinds[in.Kind]:
		return ContextItem{}, domain.Validation("unknown context item kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.Body == "":
		return ContextItem{}, domain.Validation("a context item needs a body", nil)
	case in.Author == "":
		return ContextItem{}, domain.Validation("a context item needs an author", nil)
	}
	row, err := e.queries(true).InsertContextItem(ctx, sqlitedb.InsertContextItemParams{
		ID: newID("CI-", 16), Kind: string(in.Kind), Body: in.Body, Author: in.Author, Ts: nowMillis(),
		ProjectID: in.ProjectID, TaskID: in.TaskID,
	})
	if err := found(err, "Task", in.TaskID); err != nil {
		return ContextItem{}, err
	}
	return ContextItem{ProjectID: row.ProjectID, TaskID: row.TaskID, ID: row.ID, Kind: ContextKind(row.Kind), Body: row.Body, Author: row.Author, TS: row.Ts}, nil
}

// tsCursor reads a cursor whose order is a Unix-millisecond time; "" is the first page.
func tsCursor(cursor string) (ts int64, id string, err error) {
	if cursor == "" {
		return 0, "", nil
	}
	order, id, err := DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	ts, perr := strconv.ParseInt(order, 10, 64)
	if perr != nil || id == "" {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

func listContextItems(ctx context.Context, e engine, projectID, taskID string, q ContextItemsQuery) (ContextItemsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return ContextItemsPage{}, err
	}
	limit := clamp(q.Limit, 50, 200)
	p := sqlitedb.ListContextItemsNewestParams{AfterID: afterID, AfterTs: afterTS, ProjectID: projectID, TaskID: taskID, PageLimit: int64(limit + 1)}
	var rows []sqlitedb.ListContextItemsNewestRow
	if q.Ascending {
		var asc []sqlitedb.ListContextItemsOldestRow
		asc, err = e.queries(false).ListContextItemsOldest(ctx, sqlitedb.ListContextItemsOldestParams(p))
		rows = convert(asc, func(r sqlitedb.ListContextItemsOldestRow) sqlitedb.ListContextItemsNewestRow {
			return sqlitedb.ListContextItemsNewestRow(r)
		})
	} else {
		rows, err = e.queries(false).ListContextItemsNewest(ctx, p)
	}
	if err != nil {
		return ContextItemsPage{}, fmt.Errorf("store: list context items: %w", err)
	}
	if len(rows) == 0 {
		return ContextItemsPage{}, domain.NotFound("Task", taskID)
	}
	page := ContextItemsPage{Items: []ContextItem{}}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
			break
		}
		page.Items = append(page.Items, ContextItem{
			ProjectID: projectID, TaskID: taskID, ID: r.ID.String, Kind: ContextKind(r.Kind.String),
			Body: r.Body.String, Author: r.Author.String, TS: r.Ts.Int64,
		})
	}
	return page, nil
}

func deleteContextItemsByTask(ctx context.Context, e engine, projectID, taskID string) (int64, error) {
	n, err := e.queries(true).DeleteContextItemsByTask(ctx, sqlitedb.DeleteContextItemsByTaskParams{ProjectID: projectID, TaskID: taskID})
	if err != nil {
		return 0, fmt.Errorf("store: delete context items: %w", err)
	}
	return n, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertContextItem(ctx context.Context, arg sqlitedb.InsertContextItemParams) (sqlitedb.ContextItem, error) {
	r, err := p.q.InsertContextItem(ctx, pgdb.InsertContextItemParams(arg))
	return sqlitedb.ContextItem(r), err
}

func (p pgQueries) ListContextItemsNewest(ctx context.Context, arg sqlitedb.ListContextItemsNewestParams) ([]sqlitedb.ListContextItemsNewestRow, error) {
	rows, err := p.q.ListContextItemsNewest(ctx, pgdb.ListContextItemsNewestParams(arg))
	return convert(rows, func(r pgdb.ListContextItemsNewestRow) sqlitedb.ListContextItemsNewestRow {
		return sqlitedb.ListContextItemsNewestRow(r)
	}), err
}

func (p pgQueries) ListContextItemsOldest(ctx context.Context, arg sqlitedb.ListContextItemsOldestParams) ([]sqlitedb.ListContextItemsOldestRow, error) {
	rows, err := p.q.ListContextItemsOldest(ctx, pgdb.ListContextItemsOldestParams(arg))
	return convert(rows, func(r pgdb.ListContextItemsOldestRow) sqlitedb.ListContextItemsOldestRow {
		return sqlitedb.ListContextItemsOldestRow(r)
	}), err
}

func (p pgQueries) DeleteContextItemsByTask(ctx context.Context, arg sqlitedb.DeleteContextItemsByTaskParams) (int64, error) {
	return p.q.DeleteContextItemsByTask(ctx, pgdb.DeleteContextItemsByTaskParams(arg))
}
