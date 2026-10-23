package service

import (
	"encoding/json"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// projectRows counts every event of the project's boards (a project event has no board, and the
// store has no project-level event, so this is every event the project holds), every audit row and
// every change row. A deleted project counts no events.
func (f *fixture) projectRows(t *testing.T, projectID string) rows {
	t.Helper()
	var r rows
	boards, err := f.st.ListBoards(f.ctx, projectID)
	if err == nil {
		for _, b := range boards {
			page, err := f.st.ListBoardFeed(f.ctx, projectID, b.ID, "", 200)
			if err != nil {
				t.Fatal(err)
			}
			r.events += len(page.Events)
		}
	}
	audit, err := f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	r.audit = len(audit.Rows)
	r.changes = len(f.changes(t))
	return r
}

// AC5: each Projects and Boards mutation writes exactly the event, audit row and change-log row its
// contract comment names, each at most once, and always one change row. The store's area methods
// compose nothing: the same create straight through the store writes none of the three.
func TestEveryMutationWritesItsRowsOnce(t *testing.T) {
	f := newFixture(t, Options{})
	f.addMember(t, "bob", domain.RoleWorker)
	var made struct {
		project string
		board   Board
		column  Column
	}
	steps := []struct {
		tool  string
		want  rows
		board bool // the change row names a board
		run   func() error
	}{
		// DEVIATION: the contract names a project.created event, which the store cannot append
		// (AppendBoardEvent requires a board); want.events is 0 until it can.
		{"create_project", rows{0, 0, 1}, false, func() error {
			r, err := f.c.Projects().Create(f.ctx, f.alice, CreateProjectReq{Name: "Q"})
			made.project = r.ProjectID
			return err
		}},
		{"update_settings", rows{0, 1, 1}, false, func() error {
			_, err := f.c.Projects().UpdateSettings(f.ctx, f.alice, UpdateSettingsReq{ProjectID: f.project.ID, MaxRunsPerTask: 3})
			return err
		}},
		{"add_member", rows{0, 1, 1}, false, func() error {
			_, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: "carol", Role: domain.RoleMaster})
			return err
		}},
		{"remove_member", rows{0, 1, 1}, false, func() error {
			_, err := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "bob"})
			return err
		}},
		{"create_board", rows{1, 0, 1}, true, func() error {
			r, err := f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "B2", IdempotencyKey: "b"})
			made.board = r.Board
			return err
		}},
		{"create_board replayed", rows{0, 0, 0}, false, func() error {
			r, err := f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "B2", IdempotencyKey: "b"})
			if err == nil && r.Board.ID != made.board.ID {
				t.Fatalf("the replay made board %s, first %s", r.Board.ID, made.board.ID)
			}
			return err
		}},
		{"add_column", rows{0, 0, 1}, true, func() error {
			r, err := f.c.Boards().AddColumn(f.ctx, f.alice, AddColumnReq{ProjectID: f.project.ID, BoardID: made.board.ID, Name: "QA", Status: domain.Review})
			made.column = r.Column
			return err
		}},
		{"update_column", rows{0, 0, 1}, true, func() error {
			_, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: made.board.ID, ColumnID: made.column.ID, WIPLimit: Value(int64(4))})
			return err
		}},
		{"remove_column", rows{0, 0, 1}, true, func() error {
			_, err := f.c.Boards().RemoveColumn(f.ctx, f.alice, RemoveColumnReq{ProjectID: f.project.ID, BoardID: made.board.ID, ColumnID: made.column.ID})
			return err
		}},
		{"delete_project", rows{0, 1, 1}, false, func() error {
			_, err := f.c.Projects().Delete(f.ctx, f.alice, DeleteProjectReq{ProjectID: made.project})
			return err
		}},
	}
	for _, s := range steps {
		pid := f.project.ID
		if s.tool == "create_project" || s.tool == "delete_project" {
			pid = made.project
		}
		before := f.projectRows(t, pid)
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.tool, err)
		}
		if s.tool == "create_project" {
			pid = made.project
			before.events = f.projectRows(t, pid).events // a new project held nothing before
		}
		after := f.projectRows(t, pid)
		if s.tool == "delete_project" {
			after.events = before.events // its events went with it
		}
		got := rows{after.events - before.events, after.audit - before.audit, after.changes - before.changes}
		t.Logf("%-22s events +%d  audit +%d  change_log +%d", s.tool, got.events, got.audit, got.changes)
		if got != s.want {
			t.Fatalf("%s wrote %+v, want %+v", s.tool, got, s.want)
		}
		if s.board {
			ch := f.changes(t)
			var p changePayload
			_ = json.Unmarshal(ch[len(ch)-1].Payload, &p)
			if p.BoardID != made.board.ID || p.Kind != ChangeKindChanged || ch[len(ch)-1].Scope != BroadcastScope(f.project.ID) {
				t.Fatalf("%s's change row %s names no board %s", s.tool, ch[len(ch)-1].Payload, made.board.ID)
			}
		}
	}

	// The check line: R0.8's area method composes nothing.
	before := f.projectRows(t, f.project.ID)
	if _, err := f.st.CreateBoard(f.ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "raw", CreatedBy: "alice"}, store.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	after := f.projectRows(t, f.project.ID)
	t.Logf("store.CreateBoard straight: events +%d  audit +%d  change_log +%d", after.events-before.events, after.audit-before.audit, after.changes-before.changes)
	if after != before {
		t.Fatalf("store.CreateBoard wrote rows of its own: %+v -> %+v", before, after)
	}
}
