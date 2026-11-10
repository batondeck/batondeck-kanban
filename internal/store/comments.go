package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The comment area of contract_comments.go, on both engines. Each call is one statement: the insert
// and the page both start FROM the task, so a missing task is their own empty answer. A reply adds
// one read of its parent, in the insert's transaction.

type commentQueries interface {
	GetComment(ctx context.Context, arg sqlitedb.GetCommentParams) (sqlitedb.Comment, error)
	InsertComment(ctx context.Context, arg sqlitedb.InsertCommentParams) (sqlitedb.Comment, error)
	ListCommentsOldest(ctx context.Context, arg sqlitedb.ListCommentsOldestParams) ([]sqlitedb.ListCommentsOldestRow, error)
}

func (s *SQLite) AddComment(ctx context.Context, in AddCommentInput) (Comment, error) {
	return addComment(ctx, s, in)
}

func (s *Postgres) AddComment(ctx context.Context, in AddCommentInput) (Comment, error) {
	return addComment(ctx, s, in)
}

func (s *SQLite) ListComments(ctx context.Context, projectID, taskID string, q CommentsQuery) (CommentsPage, error) {
	return listComments(ctx, s, projectID, taskID, q)
}

func (s *Postgres) ListComments(ctx context.Context, projectID, taskID string, q CommentsQuery) (CommentsPage, error) {
	return listComments(ctx, s, projectID, taskID, q)
}

func addComment(ctx context.Context, e engine, in AddCommentInput) (Comment, error) {
	switch {
	case in.Body == "":
		return Comment{}, domain.Validation("a comment needs a body", nil)
	case in.Author == "":
		return Comment{}, domain.Validation("a comment needs an author", nil)
	}
	var row sqlitedb.Comment
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		var err error
		row, err = q.InsertComment(ctx, sqlitedb.InsertCommentParams{
			ID: newID("CM-", 16), Author: in.Author, Body: in.Body, ParentCommentID: nullStr(in.ParentCommentID),
			Mentions: jsonList(in.Mentions), Ts: nowMillis(), ProjectID: in.ProjectID, TaskID: in.TaskID,
		})
		if err := found(err, "Task", in.TaskID); err != nil {
			return err
		}
		if p := in.ParentCommentID; p != nil {
			// After the insert, so a missing task is still NotFound Task; a missing parent rolls it back.
			_, err := q.GetComment(ctx, sqlitedb.GetCommentParams{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: *p})
			return found(err, "Comment", *p)
		}
		return nil
	})
	if err != nil {
		return Comment{}, err
	}
	mentions, err := readList(row.Mentions)
	if err != nil {
		return Comment{}, fmt.Errorf("store: comment mentions: %w", err)
	}
	return Comment{ProjectID: row.ProjectID, TaskID: row.TaskID, ID: row.ID, Author: row.Author, Body: row.Body,
		ParentCommentID: strPtr(row.ParentCommentID), Mentions: mentions, TS: row.Ts}, nil
}

func listComments(ctx context.Context, e engine, projectID, taskID string, q CommentsQuery) (CommentsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return CommentsPage{}, err
	}
	limit := clamp(q.Limit, 100, 200)
	rows, err := e.queries(false).ListCommentsOldest(ctx, sqlitedb.ListCommentsOldestParams{
		AfterID: afterID, AfterTs: afterTS, ProjectID: projectID, TaskID: taskID, PageLimit: int64(limit + 1)})
	if err != nil {
		return CommentsPage{}, fmt.Errorf("store: list comments: %w", err)
	}
	if len(rows) == 0 {
		return CommentsPage{}, domain.NotFound("Task", taskID)
	}
	page := CommentsPage{Items: []Comment{}}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
			break
		}
		mentions, err := readList(r.Mentions.String)
		if err != nil {
			return CommentsPage{}, fmt.Errorf("store: comment mentions: %w", err)
		}
		page.Items = append(page.Items, Comment{ProjectID: projectID, TaskID: taskID, ID: r.ID.String, Author: r.Author.String,
			Body: r.Body.String, ParentCommentID: strPtr(r.ParentCommentID), Mentions: mentions, TS: r.Ts.Int64})
	}
	return page, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) GetComment(ctx context.Context, arg sqlitedb.GetCommentParams) (sqlitedb.Comment, error) {
	r, err := p.q.GetComment(ctx, pgdb.GetCommentParams(arg))
	return sqlitedb.Comment(r), err
}

func (p pgQueries) InsertComment(ctx context.Context, arg sqlitedb.InsertCommentParams) (sqlitedb.Comment, error) {
	r, err := p.q.InsertComment(ctx, pgdb.InsertCommentParams(arg))
	return sqlitedb.Comment(r), err
}

func (p pgQueries) ListCommentsOldest(ctx context.Context, arg sqlitedb.ListCommentsOldestParams) ([]sqlitedb.ListCommentsOldestRow, error) {
	rows, err := p.q.ListCommentsOldest(ctx, pgdb.ListCommentsOldestParams(arg))
	return convert(rows, func(r pgdb.ListCommentsOldestRow) sqlitedb.ListCommentsOldestRow {
		return sqlitedb.ListCommentsOldestRow(r)
	}), err
}
