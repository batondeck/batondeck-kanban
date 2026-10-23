package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

type boardQueries interface {
	CreateBoard(ctx context.Context, arg sqlitedb.CreateBoardParams) error
	GetBoard(ctx context.Context, arg sqlitedb.GetBoardParams) (sqlitedb.Board, error)
	LockBoard(ctx context.Context, arg sqlitedb.LockBoardParams) (sqlitedb.Board, error)
	ListBoards(ctx context.Context, projectID string) ([]sqlitedb.Board, error)
	CreateColumn(ctx context.Context, arg sqlitedb.CreateColumnParams) error
	ListColumns(ctx context.Context, arg sqlitedb.ListColumnsParams) ([]sqlitedb.Column, error)
	ListProjectColumns(ctx context.Context, projectID string) ([]sqlitedb.Column, error)
	ListColumnsWithTaskCounts(ctx context.Context, arg sqlitedb.ListColumnsWithTaskCountsParams) ([]sqlitedb.ListColumnsWithTaskCountsRow, error)
	UpdateColumn(ctx context.Context, arg sqlitedb.UpdateColumnParams) error
	DeleteColumn(ctx context.Context, arg sqlitedb.DeleteColumnParams) error
	CountColumnTasks(ctx context.Context, arg sqlitedb.CountColumnTasksParams) (int64, error)
}

func (s *SQLite) CreateBoard(ctx context.Context, in CreateBoardInput, opts WriteOptions) (Board, error) {
	return createBoard(ctx, s, in, opts)
}

func (s *Postgres) CreateBoard(ctx context.Context, in CreateBoardInput, opts WriteOptions) (Board, error) {
	return createBoard(ctx, s, in, opts)
}

func (s *SQLite) GetBoard(ctx context.Context, projectID, boardID string) (Board, TaskCounts, error) {
	return getBoard(ctx, s, projectID, boardID)
}

func (s *Postgres) GetBoard(ctx context.Context, projectID, boardID string) (Board, TaskCounts, error) {
	return getBoard(ctx, s, projectID, boardID)
}

func (s *SQLite) ListBoards(ctx context.Context, projectID string) ([]Board, error) {
	return listBoards(ctx, s, projectID)
}

func (s *Postgres) ListBoards(ctx context.Context, projectID string) ([]Board, error) {
	return listBoards(ctx, s, projectID)
}

func (s *SQLite) AddColumn(ctx context.Context, projectID, boardID string, in ColumnInput, opts WriteOptions) (Column, error) {
	return addColumn(ctx, s, projectID, boardID, in, opts)
}

func (s *Postgres) AddColumn(ctx context.Context, projectID, boardID string, in ColumnInput, opts WriteOptions) (Column, error) {
	return addColumn(ctx, s, projectID, boardID, in, opts)
}

func (s *SQLite) UpdateColumn(ctx context.Context, projectID, boardID, columnID string, patch ColumnPatch) (Board, error) {
	return updateColumn(ctx, s, projectID, boardID, columnID, patch)
}

func (s *Postgres) UpdateColumn(ctx context.Context, projectID, boardID, columnID string, patch ColumnPatch) (Board, error) {
	return updateColumn(ctx, s, projectID, boardID, columnID, patch)
}

func (s *SQLite) RemoveColumn(ctx context.Context, projectID, boardID, columnID string) (Board, error) {
	return removeColumn(ctx, s, projectID, boardID, columnID)
}

func (s *Postgres) RemoveColumn(ctx context.Context, projectID, boardID, columnID string) (Board, error) {
	return removeColumn(ctx, s, projectID, boardID, columnID)
}

// defaultColumns is a new board's columns when the caller names none: conductor's defaults
// (boards.ts:19-27) without Cancelled, in the plan's state order, and without Review when the
// project's reviewEnabled is off (conductor's defaultColumnsFor).
func defaultColumns(reviewEnabled bool) []ColumnInput {
	all := []ColumnInput{
		{Name: "To do", Status: domain.Todo},
		{Name: "In progress", Status: domain.InProgress},
		{Name: "Blocked", Status: domain.Blocked},
		{Name: "Review", Status: domain.Review},
		{Name: "Done", Status: domain.Done},
	}
	if reviewEnabled {
		return all
	}
	return slices.DeleteFunc(all, func(c ColumnInput) bool { return c.Status == domain.Review })
}

func validColumn(c ColumnInput) error {
	if strings.TrimSpace(c.Name) == "" {
		return domain.Validation("a column needs a name", nil)
	}
	if _, err := domain.ParseStatus(string(c.Status)); err != nil {
		return err
	}
	if c.WIPLimit != nil && *c.WIPLimit <= 0 {
		return domain.Validation("a WIP limit must be positive", map[string]any{"wipLimit": *c.WIPLimit})
	}
	return nil
}

func createBoard(ctx context.Context, e engine, in CreateBoardInput, opts WriteOptions) (Board, error) {
	if strings.TrimSpace(in.Name) == "" || in.CreatedBy == "" {
		return Board{}, domain.Validation("a board needs a name and a creator", nil)
	}
	if in.Columns != nil && len(in.Columns) == 0 {
		return Board{}, domain.Validation("a board needs at least one column", nil)
	}
	for _, c := range in.Columns {
		if err := validColumn(c); err != nil {
			return Board{}, err
		}
	}
	return idempotent(ctx, e, in.ProjectID, opts, func(tx Store) (Board, error) {
		q := on(tx).queries(true)
		project, err := q.GetProject(ctx, in.ProjectID)
		if err := found(err, "Project", in.ProjectID); err != nil {
			return Board{}, err
		}
		cols := in.Columns
		if cols == nil {
			cols = defaultColumns(project.ReviewEnabled == 1)
		}
		row := sqlitedb.Board{ProjectID: in.ProjectID, ID: newID("B-", 12), Name: in.Name, CreatedBy: in.CreatedBy, CreatedAt: nowMillis()}
		if err := q.CreateBoard(ctx, sqlitedb.CreateBoardParams(row)); err != nil {
			return Board{}, fmt.Errorf("store: create board: %w", err)
		}
		colRows := make([]sqlitedb.Column, len(cols))
		for i, c := range cols {
			order := int64(i)
			if c.Order != nil {
				order = *c.Order
			}
			colRows[i] = sqlitedb.Column{
				ProjectID: in.ProjectID, BoardID: row.ID, ID: newID("C-", 10), Name: c.Name,
				Position: order, Status: string(c.Status), WipLimit: nullInt(c.WIPLimit),
			}
			if err := q.CreateColumn(ctx, sqlitedb.CreateColumnParams(colRows[i])); err != nil {
				return Board{}, fmt.Errorf("store: create board: %w", err)
			}
		}
		return boardFromRows(row, colRows), nil
	})
}

func getBoard(ctx context.Context, e engine, projectID, boardID string) (Board, TaskCounts, error) {
	q := e.queries(false)
	row, err := q.GetBoard(ctx, sqlitedb.GetBoardParams{ProjectID: projectID, ID: boardID})
	if err := found(err, "Board", boardID); err != nil {
		return Board{}, nil, err
	}
	rows, err := q.ListColumnsWithTaskCounts(ctx, sqlitedb.ListColumnsWithTaskCountsParams{ProjectID: projectID, BoardID: boardID})
	if err != nil {
		return Board{}, nil, fmt.Errorf("store: get board: %w", err)
	}
	cols := make([]sqlitedb.Column, len(rows))
	counts := make(TaskCounts, len(rows))
	for i, r := range rows {
		cols[i] = sqlitedb.Column{
			ProjectID: r.ProjectID, BoardID: r.BoardID, ID: r.ID, Name: r.Name,
			Position: r.Position, Status: r.Status, WipLimit: r.WipLimit,
		}
		counts[r.ID] = r.TaskCount
	}
	return boardFromRows(row, cols), counts, nil
}

func listBoards(ctx context.Context, e engine, projectID string) ([]Board, error) {
	q := e.queries(false)
	rows, err := q.ListBoards(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list boards: %w", err)
	}
	if len(rows) == 0 {
		if _, err := q.GetProject(ctx, projectID); err != nil {
			return nil, found(err, "Project", projectID)
		}
		return []Board{}, nil
	}
	cols, err := q.ListProjectColumns(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list boards: %w", err)
	}
	byBoard := map[string][]sqlitedb.Column{}
	for _, c := range cols {
		byBoard[c.BoardID] = append(byBoard[c.BoardID], c)
	}
	out := make([]Board, len(rows))
	for i, r := range rows {
		out[i] = boardFromRows(r, byBoard[r.ID])
	}
	return out, nil
}

func addColumn(ctx context.Context, e engine, projectID, boardID string, in ColumnInput, opts WriteOptions) (Column, error) {
	if err := validColumn(in); err != nil {
		return Column{}, err
	}
	return idempotent(ctx, e, projectID, opts, func(tx Store) (Column, error) {
		q := on(tx).queries(true)
		_, cols, err := lockBoard(ctx, q, projectID, boardID)
		if err != nil {
			return Column{}, err
		}
		order := int64(0)
		if in.Order != nil {
			order = *in.Order
		} else if len(cols) > 0 {
			order = slices.MaxFunc(cols, func(a, b sqlitedb.Column) int { return cmp.Compare(a.Position, b.Position) }).Position + 1
		}
		row := sqlitedb.Column{
			ProjectID: projectID, BoardID: boardID, ID: newID("C-", 10), Name: in.Name,
			Position: order, Status: string(in.Status), WipLimit: nullInt(in.WIPLimit),
		}
		if err := q.CreateColumn(ctx, sqlitedb.CreateColumnParams(row)); err != nil {
			return Column{}, fmt.Errorf("store: add column: %w", err)
		}
		return columnFromRow(row), nil
	})
}

func updateColumn(ctx context.Context, e engine, projectID, boardID, columnID string, patch ColumnPatch) (Board, error) {
	var out Board
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		board, cols, err := lockBoard(ctx, q, projectID, boardID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(cols, func(c sqlitedb.Column) bool { return c.ID == columnID })
		if i < 0 {
			return domain.NotFound("Column", columnID)
		}
		c := columnFromRow(cols[i])
		next := ColumnInput{Name: c.Name, Status: c.Status, WIPLimit: c.WIPLimit, Order: &c.Order}
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if patch.SetWIPLimit {
			next.WIPLimit = patch.WIPLimit
		}
		if patch.Order != nil {
			next.Order = patch.Order
		}
		if err := validColumn(next); err != nil {
			return err
		}
		cols[i].Name, cols[i].Status, cols[i].WipLimit, cols[i].Position = next.Name, string(next.Status), nullInt(next.WIPLimit), *next.Order
		if err := q.UpdateColumn(ctx, sqlitedb.UpdateColumnParams{
			Name: cols[i].Name, Position: cols[i].Position, Status: cols[i].Status, WipLimit: cols[i].WipLimit,
			ProjectID: projectID, BoardID: boardID, ID: columnID,
		}); err != nil {
			return fmt.Errorf("store: update column: %w", err)
		}
		out = boardFromRows(board, cols)
		return nil
	})
	return out, err
}

func removeColumn(ctx context.Context, e engine, projectID, boardID, columnID string) (Board, error) {
	var out Board
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		board, cols, err := lockBoard(ctx, q, projectID, boardID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(cols, func(c sqlitedb.Column) bool { return c.ID == columnID })
		if i < 0 {
			return domain.NotFound("Column", columnID)
		}
		n, err := q.CountColumnTasks(ctx, sqlitedb.CountColumnTasksParams{ProjectID: projectID, BoardID: boardID, ColumnID: columnID})
		if err != nil {
			return fmt.Errorf("store: remove column: %w", err)
		}
		if n > 0 {
			return domain.Validation("Cannot remove a column that still has tasks — move them first", map[string]any{"columnId": columnID, "tasks": n})
		}
		if len(cols) <= 1 {
			return domain.Validation("Cannot remove the last column", map[string]any{"columnId": columnID})
		}
		if err := q.DeleteColumn(ctx, sqlitedb.DeleteColumnParams{ProjectID: projectID, BoardID: boardID, ID: columnID}); err != nil {
			return fmt.Errorf("store: remove column: %w", err)
		}
		out = boardFromRows(board, slices.Delete(cols, i, i+1))
		return nil
	})
	return out, err
}

// lockBoard reads the board, locked to the transaction on Postgres, and its columns.
func lockBoard(ctx context.Context, q sqlQueries, projectID, boardID string) (sqlitedb.Board, []sqlitedb.Column, error) {
	board, err := q.LockBoard(ctx, sqlitedb.LockBoardParams{ProjectID: projectID, ID: boardID})
	if err := found(err, "Board", boardID); err != nil {
		return sqlitedb.Board{}, nil, err
	}
	cols, err := q.ListColumns(ctx, sqlitedb.ListColumnsParams{ProjectID: projectID, BoardID: boardID})
	if err != nil {
		return sqlitedb.Board{}, nil, fmt.Errorf("store: board columns: %w", err)
	}
	return board, cols, nil
}

// boardFromRows is the board with its columns in order: position, then id, as the queries read them.
func boardFromRows(b sqlitedb.Board, cols []sqlitedb.Column) Board {
	out := Board{ProjectID: b.ProjectID, ID: b.ID, Name: b.Name, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt, Columns: make([]Column, len(cols))}
	for i, c := range cols {
		out.Columns[i] = columnFromRow(c)
	}
	slices.SortStableFunc(out.Columns, func(a, b Column) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func columnFromRow(c sqlitedb.Column) Column {
	return Column{ID: c.ID, Name: c.Name, Order: c.Position, Status: domain.Status(c.Status), WIPLimit: intPtr(c.WipLimit)}
}

func (p pgQueries) CreateBoard(ctx context.Context, arg sqlitedb.CreateBoardParams) error {
	return p.q.CreateBoard(ctx, pgdb.CreateBoardParams(arg))
}

func (p pgQueries) GetBoard(ctx context.Context, arg sqlitedb.GetBoardParams) (sqlitedb.Board, error) {
	r, err := p.q.GetBoard(ctx, pgdb.GetBoardParams(arg))
	return sqlitedb.Board(r), err
}

func (p pgQueries) LockBoard(ctx context.Context, arg sqlitedb.LockBoardParams) (sqlitedb.Board, error) {
	r, err := p.q.LockBoard(ctx, pgdb.LockBoardParams(arg))
	return sqlitedb.Board(r), err
}

func (p pgQueries) ListBoards(ctx context.Context, projectID string) ([]sqlitedb.Board, error) {
	rows, err := p.q.ListBoards(ctx, projectID)
	return convert(rows, func(r pgdb.Board) sqlitedb.Board { return sqlitedb.Board(r) }), err
}

func (p pgQueries) CreateColumn(ctx context.Context, arg sqlitedb.CreateColumnParams) error {
	return p.q.CreateColumn(ctx, pgdb.CreateColumnParams(arg))
}

func (p pgQueries) ListColumns(ctx context.Context, arg sqlitedb.ListColumnsParams) ([]sqlitedb.Column, error) {
	rows, err := p.q.ListColumns(ctx, pgdb.ListColumnsParams(arg))
	return convert(rows, func(r pgdb.Column) sqlitedb.Column { return sqlitedb.Column(r) }), err
}

func (p pgQueries) ListProjectColumns(ctx context.Context, projectID string) ([]sqlitedb.Column, error) {
	rows, err := p.q.ListProjectColumns(ctx, projectID)
	return convert(rows, func(r pgdb.Column) sqlitedb.Column { return sqlitedb.Column(r) }), err
}

func (p pgQueries) ListColumnsWithTaskCounts(ctx context.Context, arg sqlitedb.ListColumnsWithTaskCountsParams) ([]sqlitedb.ListColumnsWithTaskCountsRow, error) {
	rows, err := p.q.ListColumnsWithTaskCounts(ctx, pgdb.ListColumnsWithTaskCountsParams(arg))
	return convert(rows, func(r pgdb.ListColumnsWithTaskCountsRow) sqlitedb.ListColumnsWithTaskCountsRow {
		return sqlitedb.ListColumnsWithTaskCountsRow(r)
	}), err
}

func (p pgQueries) UpdateColumn(ctx context.Context, arg sqlitedb.UpdateColumnParams) error {
	return p.q.UpdateColumn(ctx, pgdb.UpdateColumnParams(arg))
}

func (p pgQueries) DeleteColumn(ctx context.Context, arg sqlitedb.DeleteColumnParams) error {
	return p.q.DeleteColumn(ctx, pgdb.DeleteColumnParams(arg))
}

func (p pgQueries) CountColumnTasks(ctx context.Context, arg sqlitedb.CountColumnTasksParams) (int64, error) {
	return p.q.CountColumnTasks(ctx, pgdb.CountColumnTasksParams(arg))
}
