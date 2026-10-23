package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// AC2: a board created with no columns has the six default columns, none of them Cancelled.
func TestCreateBoardDefaultColumns(t *testing.T) {
	f := newFixture(t, Options{})
	got, err := f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var statuses []domain.Status
	for _, c := range got.Board.Columns {
		names = append(names, c.Name)
		statuses = append(statuses, c.Status)
	}
	t.Logf("create_board columns: %v %v", names, statuses)
	if len(got.Board.Columns) != 6 || !reflect.DeepEqual(statuses, domain.AllStatuses()) {
		t.Fatalf("columns %v %v, want the six statuses", names, statuses)
	}
	for _, n := range names {
		if strings.EqualFold(n, "cancelled") {
			t.Fatalf("a Cancelled column: %v", names)
		}
	}
	// Positive control: named columns are kept as given, in order.
	got, err = f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "Two", Columns: []ColumnSpec{{Name: "Todo", Status: domain.Ready, WIPLimit: ptr(int64(3))}, {Name: "Done", Status: domain.Done}}})
	if err != nil || len(got.Board.Columns) != 2 || got.Board.Columns[0].Name != "Todo" || *got.Board.Columns[0].WIPLimit != 3 || got.Board.Columns[1].WIPLimit != nil {
		t.Fatalf("named columns %+v (err %v)", got.Board.Columns, err)
	}
	_, err = f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "Bad", Columns: []ColumnSpec{{Name: "X", Status: "CANCELLED"}}})
	wantCode(t, err, domain.CodeValidation)
}

// fixedTable is the plan's §Ticket states table, written out rather than derived, so a change to
// the domain's table fails here.
var fixedTable = map[string][]domain.Status{
	"BACKLOG":     {domain.Ready},
	"READY":       {domain.InProgress, domain.Backlog},
	"IN_PROGRESS": {domain.Review, domain.Blocked, domain.Ready},
	"BLOCKED":     {domain.InProgress, domain.Ready},
	"REVIEW":      {domain.Done, domain.InProgress},
	"DONE":        {domain.InProgress},
}

// AC4: get_board's store primitives cost one GetBoard (two statements by its contract: the board,
// and its columns joined to one grouped count), and its transitions are the fixed table.
func TestGetBoardCostsTwoStatements(t *testing.T) {
	f, n := counted(t)
	f.task(t, domain.Ready, nil)
	f.task(t, domain.Ready, nil)
	if _, err := f.c.Boards().List(f.ctx, f.alice, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
		t.Fatal(err) // warms alice's access entry
	}
	n.take()
	got, err := f.c.Boards().Get(f.ctx, f.alice, GetBoardReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	if err != nil {
		t.Fatal(err)
	}
	reads := n.take()
	t.Logf("get_board store calls %v; taskCounts %v", reads, got.TaskCounts)
	if !reflect.DeepEqual(reads, map[string]int{"GetBoard": 1}) {
		t.Fatalf("get_board made %v, want one GetBoard (2 statements)", reads)
	}
	if got.TaskCounts[f.column(t, domain.Ready)] != 2 || len(got.TaskCounts) != 6 {
		t.Fatalf("taskCounts %v", got.TaskCounts)
	}
	if !reflect.DeepEqual(got.Board.Transitions, fixedTable) {
		t.Fatalf("transitions %v, want the fixed table", got.Board.Transitions)
	}
	list, _ := f.c.Boards().List(f.ctx, f.alice, ListBoardsReq{ProjectID: f.project.ID})
	if !reflect.DeepEqual(list.Boards[0].Transitions, fixedTable) {
		t.Fatalf("list_boards transitions %v", list.Boards[0].Transitions)
	}
}

// remove_column is refused while the column holds a task, and removes it once empty.
func TestRemoveColumnRefusedWhileItHoldsTasks(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Ready, nil)
	col := f.column(t, domain.Ready)
	_, err := f.c.Boards().RemoveColumn(f.ctx, f.alice, RemoveColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col})
	t.Logf("remove_column holding %s: %v", task.ID, err)
	wantCode(t, err, domain.CodeValidation)
	empty := f.column(t, domain.Blocked)
	got, err := f.c.Boards().RemoveColumn(f.ctx, f.alice, RemoveColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: empty})
	if err != nil || len(got.Board.Columns) != 5 {
		t.Fatalf("remove_column of an empty column: %+v (err %v)", got.Board.Columns, err)
	}
	// Structural tier: a master cannot.
	dave := f.member(t, "dave", domain.RoleMaster)
	_, err = f.c.Boards().RemoveColumn(f.ctx, dave, RemoveColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col})
	wantCode(t, err, domain.CodeForbidden)
}

func TestColumns(t *testing.T) {
	f := newFixture(t, Options{})
	add, err := f.c.Boards().AddColumn(f.ctx, f.alice, AddColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "QA", Status: domain.Review, WIPLimit: ptr(int64(2)), IdempotencyKey: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.c.Boards().AddColumn(f.ctx, f.alice, AddColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "QA", Status: domain.Review, IdempotencyKey: "c1"})
	if err != nil || again.Column.ID != add.Column.ID {
		t.Fatalf("the add_column replay made %s, first %s (err %v)", again.Column.ID, add.Column.ID, err)
	}
	upd, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: add.Column.ID, WIPLimit: Value(int64(5)), Name: "QA2"})
	if err != nil {
		t.Fatal(err)
	}
	var got Column
	for _, c := range upd.Board.Columns {
		if c.ID == add.Column.ID {
			got = c
		}
	}
	if got.Name != "QA2" || got.WIPLimit == nil || *got.WIPLimit != 5 || len(upd.Board.Columns) != 7 {
		t.Fatalf("update_column %+v", upd.Board.Columns)
	}
	for _, c := range []struct {
		name string
		err  error
	}{
		{"a fractional order", func() error {
			_, err := f.c.Boards().AddColumn(f.ctx, f.alice, AddColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "x", Status: domain.Ready, Order: Some(1.5)})
			return err
		}()},
		{"a zero wipLimit", func() error {
			_, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: add.Column.ID, WIPLimit: Value(int64(0))})
			return err
		}()},
		{"an unknown status", func() error {
			_, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: add.Column.ID, Status: "CANCELLED"})
			return err
		}()},
	} {
		t.Logf("%s: %v", c.name, c.err)
		wantCode(t, c.err, domain.CodeValidation)
	}
	_, err = f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: "C-missing", Name: "n"})
	wantCode(t, err, domain.CodeNotFound)
	_, err = f.c.Boards().Get(f.ctx, f.alice, GetBoardReq{ProjectID: f.project.ID, BoardID: "B-missing"})
	wantCode(t, err, domain.CodeNotFound)
}
