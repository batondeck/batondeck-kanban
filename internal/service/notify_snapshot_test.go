package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-260: a notification's change row names its board, so a process the bus delivers it to drops
// that board's snapshot alone: a mention on a task of board B2 leaves board B's snapshot warm.
func TestNotificationRowKeepsAnotherBoardsSnapshot(t *testing.T) {
	f, n := counted(t)
	f.wire()
	if _, err := f.st.AddMember(f.ctx, f.project.ID, "grace", domain.RoleMember, "alice"); err != nil {
		t.Fatal(err)
	}
	b2, err := f.st.CreateBoard(f.ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "B2", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var todo string
	for _, c := range b2.Columns {
		if c.Status == domain.Todo {
			todo = c.ID
		}
	}
	task, err := f.st.CreateTask(f.ctx, store.CreateTaskInput{ProjectID: f.project.ID, BoardID: b2.ID, ColumnID: todo, Title: "t", Status: domain.Todo,
		OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	before := f.changes(t)
	f.snapshot(t) // board B, warm
	n.take()
	if _, err := f.c.Comments().AddComment(f.ctx, f.alice, AddCommentReq{ProjectID: f.project.ID, TaskID: task.ID, Body: "@grace have a look"}); err != nil {
		t.Fatal(err)
	}
	var rows []store.Change
	for _, ch := range f.changes(t)[len(before):] {
		if ch.Kind == ChangeKindNotification {
			rows = append(rows, ch)
		}
		f.c.ObserveChange(ch) // the bus delivering every row of the call
	}
	f.snapshot(t)
	reads := n.take()
	t.Logf("%d notification row(s); board B's snapshot after observing them: %v", len(rows), reads)
	if len(rows) != 1 || reads["GetBoard"] != 0 {
		t.Fatalf("%d notification rows; board B re-read %d time(s): a mention on %s dropped another board's snapshot", len(rows), reads["GetBoard"], strings.TrimSpace(b2.ID))
	}
	var p changePayload
	_ = json.Unmarshal(rows[0].Payload, &p)
	if rows[0].Scope != "grace" || p.BoardID != b2.ID || p.TaskID != task.ID {
		t.Fatalf("notification row %s scope %s, want grace's naming %s on %s", rows[0].Payload, rows[0].Scope, task.ID, b2.ID)
	}
}
