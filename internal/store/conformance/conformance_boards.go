package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func newBoard(t *testing.T, s store.Store, projectID string, cols []store.ColumnInput) store.Board {
	t.Helper()
	b, err := s.CreateBoard(context.Background(), store.CreateBoardInput{ProjectID: projectID, Name: "board", CreatedBy: "alice", Columns: cols}, store.WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func columnShape(cols []store.Column) [][2]string {
	out := make([][2]string, len(cols))
	for i, c := range cols {
		out[i] = [2]string{c.Name, string(c.Status)}
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// boards is the board and column area: default and given columns, reads with task counts, lists,
// and column add, update and remove, each with its refusals. Counts over real tasks, and removing a
// column that holds one, are internal/store's boards_test.go until the tasks area can create one.
func boards(t *testing.T, newStore Factory) {
	ctx := context.Background()
	defaults := [][2]string{
		{"Backlog", "BACKLOG"}, {"Ready", "READY"}, {"In progress", "IN_PROGRESS"},
		{"Blocked", "BLOCKED"}, {"Review", "REVIEW"}, {"Done", "DONE"},
	}

	t.Run("CreateBoardGivesTheSixDefaultColumnsInOrderAndNoCancelled", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		if got := columnShape(b.Columns); !slices.Equal(got, defaults) {
			t.Fatalf("default columns %v, want %v", got, defaults)
		}
		read, counts, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := columnShape(read.Columns); !slices.Equal(got, defaults) {
			t.Fatalf("GetBoard columns %v, want %v", got, defaults)
		}
		if len(counts) != 6 {
			t.Fatalf("task counts %v, want all six columns", counts)
		}
		for _, c := range read.Columns {
			if n, ok := counts[c.ID]; !ok || n != 0 {
				t.Fatalf("task counts %v: column %s should be a present 0", counts, c.ID)
			}
		}
	})

	t.Run("CreateBoardLeavesOutReviewWhenTheProjectHasReviewOff", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		if _, err := s.UpdateSettings(ctx, p.ID, store.SettingsPatch{"reviewEnabled": json.RawMessage(`false`)}); err != nil {
			t.Fatal(err)
		}
		b := newBoard(t, s, p.ID, nil)
		want := slices.DeleteFunc(slices.Clone(defaults), func(c [2]string) bool { return c[1] == "REVIEW" })
		if got := columnShape(b.Columns); !slices.Equal(got, want) {
			t.Fatalf("columns %v, want %v", got, want)
		}
	})

	t.Run("CreateBoardKeepsTheGivenColumnsInOrder", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, []store.ColumnInput{
			{Name: "Todo", Status: domain.Ready, WIPLimit: ptr[int64](3)},
			{Name: "Doing", Status: domain.InProgress},
		})
		read, _, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := columnShape(read.Columns); !slices.Equal(got, [][2]string{{"Todo", "READY"}, {"Doing", "IN_PROGRESS"}}) {
			t.Fatalf("columns %v", got)
		}
		if read.Columns[0].WIPLimit == nil || *read.Columns[0].WIPLimit != 3 || read.Columns[1].WIPLimit != nil {
			t.Fatalf("WIP limits %+v", read.Columns)
		}
	})

	t.Run("CreateBoardRefusesBadColumnsAndAMissingProject", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		for _, cols := range [][]store.ColumnInput{
			{},
			{{Name: "Gone", Status: "CANCELLED"}},
			{{Name: "", Status: domain.Ready}},
			{{Name: "Zero", Status: domain.Ready, WIPLimit: ptr[int64](0)}},
		} {
			_, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice", Columns: cols}, store.WriteOptions{Identity: "alice"})
			wantCode(t, err, domain.CodeValidation, "")
		}
		_, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: "P-missing", Name: "b", CreatedBy: "alice"}, store.WriteOptions{Identity: "alice"})
		wantCode(t, err, domain.CodeNotFound, "Project")
		boards, err := s.ListBoards(ctx, p.ID)
		if err != nil || len(boards) != 0 {
			t.Fatalf("boards after the refusals %+v, %v: want none", boards, err)
		}
	})

	t.Run("CreateBoardReplaysItsIdempotencyKey", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "k", IdempotencyTTL: time.Hour}
		in := store.CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}
		first, err := s.CreateBoard(ctx, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.CreateBoard(ctx, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		boards, err := s.ListBoards(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if second.ID != first.ID || len(second.Columns) != 6 || second.Columns[0].ID != first.Columns[0].ID || len(boards) != 1 {
			t.Fatalf("replay %+v of %+v, boards %d: want the same board, one row", second, first, len(boards))
		}
	})

	t.Run("GetBoardIsKeyedByProject", func(t *testing.T) {
		s := migrated(t, newStore)
		a := newProject(t, s, "alice")
		b := newProject(t, s, "alice")
		board := newBoard(t, s, a.ID, nil)
		_, _, err := s.GetBoard(ctx, b.ID, board.ID)
		wantCode(t, err, domain.CodeNotFound, "Board")
		_, _, err = s.GetBoard(ctx, a.ID, "B-missing")
		wantCode(t, err, domain.CodeNotFound, "Board")
	})

	t.Run("ListBoardsIsEveryBoardWithItsColumnsOldestFirst", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		other := newProject(t, s, "alice")
		none, err := s.ListBoards(ctx, p.ID)
		if err != nil || len(none) != 0 {
			t.Fatalf("a new project's boards %+v, %v: want none", none, err)
		}
		first := newBoard(t, s, p.ID, nil)
		time.Sleep(2 * time.Millisecond)
		second := newBoard(t, s, p.ID, []store.ColumnInput{{Name: "Only", Status: domain.Backlog}})
		newBoard(t, s, other.ID, nil)
		got, err := s.ListBoards(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID || len(got[0].Columns) != 6 || len(got[1].Columns) != 1 {
			t.Fatalf("boards %+v, want %s (6 columns) then %s (1)", got, first.ID, second.ID)
		}
		_, err = s.ListBoards(ctx, "P-missing")
		wantCode(t, err, domain.CodeNotFound, "Project")
	})

	t.Run("AddColumnAppendsAfterTheLastOrAtTheGivenOrder", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		c, err := s.AddColumn(ctx, p.ID, b.ID, store.ColumnInput{Name: "QA", Status: domain.Review, WIPLimit: ptr[int64](2)}, store.WriteOptions{Identity: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		if c.Order != 6 || c.Name != "QA" || c.Status != domain.Review || c.WIPLimit == nil || *c.WIPLimit != 2 {
			t.Fatalf("AddColumn = %+v, want order 6 after the six defaults", c)
		}
		first, err := s.AddColumn(ctx, p.ID, b.ID, store.ColumnInput{Name: "Inbox", Status: domain.Backlog, Order: ptr[int64](-1)}, store.WriteOptions{Identity: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		read, counts, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(read.Columns) != 8 || read.Columns[0].ID != first.ID || read.Columns[7].ID != c.ID || counts[c.ID] != 0 {
			t.Fatalf("columns %+v, want Inbox first and QA last", read.Columns)
		}
	})

	t.Run("AddColumnReplaysItsIdempotencyKey", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "col", IdempotencyTTL: time.Hour}
		in := store.ColumnInput{Name: "QA", Status: domain.Review}
		first, err := s.AddColumn(ctx, p.ID, b.ID, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.AddColumn(ctx, p.ID, b.ID, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		read, _, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if second != first || len(read.Columns) != 7 {
			t.Fatalf("replay %+v of %+v with %d columns: want the same column, one row", second, first, len(read.Columns))
		}
	})

	t.Run("AddColumnRefusesBadInputAndAMissingBoard", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		for _, in := range []store.ColumnInput{
			{Name: "x", Status: "DEAD_LETTER"},
			{Name: " ", Status: domain.Ready},
			{Name: "x", Status: domain.Ready, WIPLimit: ptr[int64](-2)},
		} {
			_, err := s.AddColumn(ctx, p.ID, b.ID, in, store.WriteOptions{Identity: "alice"})
			wantCode(t, err, domain.CodeValidation, "")
		}
		_, err := s.AddColumn(ctx, p.ID, "B-missing", store.ColumnInput{Name: "x", Status: domain.Ready}, store.WriteOptions{Identity: "alice"})
		wantCode(t, err, domain.CodeNotFound, "Board")
	})

	t.Run("UpdateColumnChangesTheNamedFieldsAndReorders", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		done := b.Columns[5]
		got, err := s.UpdateColumn(ctx, p.ID, b.ID, done.ID, store.ColumnPatch{
			Name: ptr("Shipped"), Order: ptr[int64](-1), SetWIPLimit: true, WIPLimit: ptr[int64](4),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Columns[0].ID != done.ID || got.Columns[0].Name != "Shipped" || got.Columns[0].Status != domain.Done || *got.Columns[0].WIPLimit != 4 {
			t.Fatalf("after the update %+v", got.Columns)
		}
		got, err = s.UpdateColumn(ctx, p.ID, b.ID, done.ID, store.ColumnPatch{Status: ptr(domain.Review), SetWIPLimit: true})
		if err != nil {
			t.Fatal(err)
		}
		read, _, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Columns[0].Status != domain.Review || got.Columns[0].WIPLimit != nil || read.Columns[0].Status != domain.Review || read.Columns[0].WIPLimit != nil || read.Columns[0].Name != "Shipped" {
			t.Fatalf("after clearing the limit %+v, read back %+v", got.Columns[0], read.Columns[0])
		}
	})

	t.Run("UpdateColumnRefusesBadInputAndAMissingColumn", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, nil)
		other := newBoard(t, s, p.ID, nil)
		col := b.Columns[0].ID
		_, err := s.UpdateColumn(ctx, p.ID, b.ID, col, store.ColumnPatch{Status: ptr[domain.Status]("CANCELLED")})
		wantCode(t, err, domain.CodeValidation, "")
		_, err = s.UpdateColumn(ctx, p.ID, b.ID, col, store.ColumnPatch{SetWIPLimit: true, WIPLimit: ptr[int64](0)})
		wantCode(t, err, domain.CodeValidation, "")
		_, err = s.UpdateColumn(ctx, p.ID, other.ID, col, store.ColumnPatch{Name: ptr("x")})
		wantCode(t, err, domain.CodeNotFound, "Column")
		_, err = s.UpdateColumn(ctx, p.ID, "B-missing", col, store.ColumnPatch{Name: ptr("x")})
		wantCode(t, err, domain.CodeNotFound, "Board")
		read, _, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil || read.Columns[0].Status != domain.Backlog || read.Columns[0].WIPLimit != nil {
			t.Fatalf("a refused update changed the column: %+v, %v", read.Columns[0], err)
		}
	})

	t.Run("RemoveColumnRemovesAnEmptyColumnButNeverTheLast", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		b := newBoard(t, s, p.ID, []store.ColumnInput{{Name: "A", Status: domain.Backlog}, {Name: "B", Status: domain.Done}})
		got, err := s.RemoveColumn(ctx, p.ID, b.ID, b.Columns[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Columns) != 1 || got.Columns[0].ID != b.Columns[1].ID {
			t.Fatalf("after the removal %+v", got.Columns)
		}
		_, err = s.RemoveColumn(ctx, p.ID, b.ID, b.Columns[1].ID)
		wantCode(t, err, domain.CodeValidation, "")
		_, err = s.RemoveColumn(ctx, p.ID, b.ID, b.Columns[0].ID)
		wantCode(t, err, domain.CodeNotFound, "Column")
		_, err = s.RemoveColumn(ctx, p.ID, "B-missing", b.Columns[1].ID)
		wantCode(t, err, domain.CodeNotFound, "Board")
		read, counts, err := s.GetBoard(ctx, p.ID, b.ID)
		if err != nil || len(read.Columns) != 1 || len(counts) != 1 {
			t.Fatalf("after the refusals %+v %v, %v: want the last column kept", read.Columns, counts, err)
		}
	})
}
