package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Column is one board column, mapped to a status. WIPLimit is nil when the column has none.
type Column struct {
	ID       string
	Name     string
	Order    int64
	Status   domain.Status
	WIPLimit *int64
}

// Board is a board with its columns in order (Order, then id). CreatedAt is Unix milliseconds.
// There is no per-board transition table: the table is fixed.
type Board struct {
	ProjectID string
	ID        string
	Name      string
	CreatedBy string
	CreatedAt int64
	Columns   []Column
}

// TaskCounts is the number of tasks in each of a board's columns, by column id. Every column of
// the board is a key, a column holding no task a 0.
type TaskCounts map[string]int64

// ColumnInput is a column to create. Order nil places it after the last column (on a new board,
// at its index in the list); WIPLimit nil is no limit, and a limit must be positive.
type ColumnInput struct {
	Name     string
	Status   domain.Status
	WIPLimit *int64
	Order    *int64
}

// CreateBoardInput is a new board. Columns nil gives the default columns: To do, In progress,
// Blocked, Review, Done (Review left out when the project's reviewEnabled is off,
// conductor's defaultColumnsFor); there is no Cancelled column. An empty, non-nil list is
// VALIDATION: a board needs a column.
type CreateBoardInput struct {
	ProjectID string
	Name      string
	CreatedBy string
	Columns   []ColumnInput
}

// ColumnPatch changes the named fields of a column; a nil field is unchanged. SetWIPLimit with a
// nil WIPLimit removes the limit.
type ColumnPatch struct {
	Name        *string
	Status      *domain.Status
	SetWIPLimit bool
	WIPLimit    *int64
	Order       *int64
}

// Boards is the board and column area. Keys: a board is (project_id, board_id), a column
// (project_id, board_id, column_id); nothing is looked up by a board or column id alone. Errors: a
// missing project is domain.NotFound("Project", id), a missing board domain.NotFound("Board", id),
// a missing column domain.NotFound("Column", id); a malformed argument, removing a column that
// holds tasks and removing a board's last column are domain.Validation. The area is versionless
// (no version column on boards or columns): no call takes an expected version or returns STALE.
// Column changes on one board take turns (the board row is locked on Postgres; SQLite has one
// writer). No list here is paginated.
type Boards interface {
	// CreateBoard creates the board and its columns in one transaction. Under
	// opts.IdempotencyKey it is replayed for (ProjectID, opts.Identity, key) within
	// opts.IdempotencyTTL.
	CreateBoard(ctx context.Context, in CreateBoardInput, opts WriteOptions) (Board, error)
	// GetBoard is the board, its columns and their task counts, in two statements: the board, and
	// its columns joined to ONE count of its tasks grouped by column.
	GetBoard(ctx context.Context, projectID, boardID string) (Board, TaskCounts, error)
	// ListBoards is the project's boards with their columns, oldest first, in two statements; a
	// missing project is NOT_FOUND, never an empty list.
	ListBoards(ctx context.Context, projectID string) ([]Board, error)
	// AddColumn adds a column to the board. Under opts.IdempotencyKey it is replayed for
	// (projectID, opts.Identity, key) within opts.IdempotencyTTL.
	AddColumn(ctx context.Context, projectID, boardID string, in ColumnInput, opts WriteOptions) (Column, error)
	// UpdateColumn applies patch to the column and returns the board after it. A status change on a
	// column that holds a task is VALIDATION and nothing changes: its tasks would disagree with it.
	// The column row is locked before the count, so a write into the column that holds it commits
	// first and is counted. On Postgres a write that read the board before the change commits and
	// locks the column after it still lands its task with the status it read; the store does not
	// compare a write's status with its column's (a task whose status no column maps keeps its own).
	UpdateColumn(ctx context.Context, projectID, boardID, columnID string, patch ColumnPatch) (Board, error)
	// RemoveColumn removes an empty column and returns the board after it. A column holding a
	// task, or the board's last column, is VALIDATION and nothing changes.
	RemoveColumn(ctx context.Context, projectID, boardID, columnID string) (Board, error)
}

var (
	_ Boards = (*SQLite)(nil)
	_ Boards = (*Postgres)(nil)
)
