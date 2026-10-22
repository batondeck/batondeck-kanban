package store

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// boardTasks is what the board tests need besides the Store: a way to put a task in a column, which
// no Store area can do until the tasks area lands. It writes the row directly, a test's own statement.
type boardTasks func(t *testing.T, projectID, boardID, columnID, taskID string)

func sqliteBoardStore(t *testing.T) (Store, boardTasks) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "boards.db")
	s, err := OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db := openSQLiteFile(t, path)
	return s, func(t *testing.T, projectID, boardID, columnID, taskID string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO tasks (project_id, id, board_id, column_id, title, status, order_key, created_by, created_at, updated_by, updated_at)
			VALUES (?, ?, ?, ?, 't', 'BACKLOG', 'a0', 'u', 1, 'u', 1)`, projectID, taskID, boardID, columnID); err != nil {
			t.Fatal(err)
		}
	}
}

func postgresBoardStore(t *testing.T) (Store, boardTasks) {
	t.Helper()
	ctx := context.Background()
	dsn := postgresDB(t)
	s, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return s, func(t *testing.T, projectID, boardID, columnID, taskID string) {
		t.Helper()
		if _, err := c.Exec(ctx, `INSERT INTO tasks (project_id, id, board_id, column_id, title, status, order_key, created_by, created_at, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, 't', 'BACKLOG', 'a0', 'u', 1, 'u', 1)`, projectID, taskID, boardID, columnID); err != nil {
			t.Fatal(err)
		}
	}
}

// boardsWithTasks: GetBoard counts every column's tasks in at most two statements (the board, and
// its columns joined to ONE grouped count; the plan's get_board <= 2), a task on another board or in
// another project is not counted, and a column holding a task cannot be removed.
func boardsWithTasks(t *testing.T, s Store, addTask boardTasks) {
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectInput{Name: "p", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, CreateProjectInput{Name: "q", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b2", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ob, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: other.ID, Name: "o", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	backlog, ready := b.Columns[0].ID, b.Columns[1].ID
	addTask(t, p.ID, b.ID, backlog, "T-1")
	addTask(t, p.ID, b.ID, backlog, "T-2")
	addTask(t, p.ID, b.ID, ready, "T-3")
	addTask(t, p.ID, b2.ID, b2.Columns[0].ID, "T-4")
	addTask(t, other.ID, ob.ID, ob.Columns[0].ID, "T-1")

	var n atomic.Int64
	board, counts, err := s.GetBoard(countingStatements(ctx, &n), p.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got > 2 {
		t.Fatalf("GetBoard issued %d statements, want at most 2", got)
	}
	t.Logf("GetBoard issued %d statements", n.Load())
	want := map[string]int64{backlog: 2, ready: 1}
	if len(counts) != len(board.Columns) {
		t.Fatalf("counts %v for %d columns", counts, len(board.Columns))
	}
	for _, c := range board.Columns {
		if counts[c.ID] != want[c.ID] {
			t.Fatalf("column %s (%s) counts %d, want %d: %v", c.ID, c.Name, counts[c.ID], want[c.ID], counts)
		}
	}

	_, err = s.RemoveColumn(ctx, p.ID, b.ID, ready)
	if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation {
		t.Fatalf("removing a column holding a task: %v, want VALIDATION", err)
	}
	after, _, err := s.GetBoard(ctx, p.ID, b.ID)
	if err != nil || len(after.Columns) != 6 {
		t.Fatalf("after the refused removal %d columns, %v: want 6", len(after.Columns), err)
	}
	// Positive control: the empty column next to it goes.
	if _, err := s.RemoveColumn(ctx, p.ID, b.ID, b.Columns[2].ID); err != nil {
		t.Fatal(err)
	}
}

func TestBoardsWithTasksSQLite(t *testing.T) {
	s, addTask := sqliteBoardStore(t)
	boardsWithTasks(t, s, addTask)
}

func TestBoardsWithTasksPostgres(t *testing.T) {
	s, addTask := postgresBoardStore(t)
	boardsWithTasks(t, s, addTask)
}
