package domain

import (
	"reflect"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

var moveBoard = Board{
	ID: "B-1", ProjectID: "P-1", Name: "Sprint",
	Columns: []Column{
		{ID: "c-backlog", Name: "Backlog", Order: 0, Status: Backlog},
		{ID: "c-ready", Name: "Ready", Order: 1, Status: Ready},
		{ID: "c-prog", Name: "In Progress", Order: 2, Status: InProgress, WIPLimit: intp(3)},
	},
}

func wantErrContaining(t *testing.T, err error, code Code, sub string) {
	t.Helper()
	e, ok := AsBatonDeckError(err)
	if !ok || e.Code != code || !strings.Contains(e.Message, sub) {
		t.Fatalf("error = %v, want %s containing %q", err, code, sub)
	}
}

func TestResolveColumnAndStatus(t *testing.T) {
	t.Run("resolves by target column (status follows column)", func(t *testing.T) {
		got, err := ResolveColumnAndStatus(moveBoard, "c-ready", "", "")
		if err != nil || got != (MoveTarget{ColumnID: "c-ready", Status: Ready}) {
			t.Fatalf("= %+v, %v", got, err)
		}
	})
	t.Run("resolves by status to the unique matching column", func(t *testing.T) {
		got, err := ResolveColumnAndStatus(moveBoard, "", InProgress, "")
		if err != nil || got != (MoveTarget{ColumnID: "c-prog", Status: InProgress}) {
			t.Fatalf("= %+v, %v", got, err)
		}
	})
	t.Run("rejects conflicting column + status", func(t *testing.T) {
		_, err := ResolveColumnAndStatus(moveBoard, "c-ready", InProgress, "")
		wantErrContaining(t, err, CodeValidation, "conflicts")
	})
	t.Run("rejects a status with no column (and no current column to fall back to)", func(t *testing.T) {
		_, err := ResolveColumnAndStatus(moveBoard, "", Done, "")
		wantErrContaining(t, err, CodeValidation, "No column")
	})
	t.Run("keeps the current column for a columnless status (e.g. CANCELLED) — in-place status change", func(t *testing.T) {
		// DEVIATION: six states (plan §Ticket states). CANCELLED does not exist; DONE, which this board
		// has no column for, is the columnless status.
		got, err := ResolveColumnAndStatus(moveBoard, "", Done, "c-backlog")
		if err != nil || got != (MoveTarget{ColumnID: "c-backlog", Status: Done}) {
			t.Fatalf("= %+v, %v", got, err)
		}
	})
	t.Run("rejects ambiguous status (>1 column)", func(t *testing.T) {
		ambiguous := Board{ID: "B-1", Columns: []Column{
			{ID: "a", Name: "A", Order: 0, Status: Ready},
			{ID: "b", Name: "B", Order: 1, Status: Ready},
		}}
		_, err := ResolveColumnAndStatus(ambiguous, "", Ready, "")
		wantErrContaining(t, err, CodeValidation, "Ambiguous")
	})
	t.Run("rejects an unknown column", func(t *testing.T) {
		_, err := ResolveColumnAndStatus(moveBoard, "nope", "", "")
		wantErrContaining(t, err, CodeNotFound, "not found")
	})
	t.Run("requires at least one of column/status", func(t *testing.T) {
		_, err := ResolveColumnAndStatus(moveBoard, "", "", "")
		wantErrContaining(t, err, CodeValidation, "requires")
	})
}

// fullBoard has a column per status; IN_PROGRESS is limited to 2.
var fullBoard = Board{ID: "B-2", Columns: []Column{
	{ID: "c-backlog", Order: 0, Status: Backlog},
	{ID: "c-ready", Order: 1, Status: Ready, WIPLimit: intp(1)},
	{ID: "c-prog", Order: 2, Status: InProgress, WIPLimit: intp(2)},
	{ID: "c-blocked", Order: 3, Status: Blocked},
	{ID: "c-review", Order: 4, Status: Review},
	{ID: "c-done", Order: 5, Status: Done},
}}

func TestDecideMove(t *testing.T) {
	for _, c := range []struct {
		name string
		req  MoveRequest
		want MoveDecision
	}{
		{"a legal move lands in the target column, appended at its end",
			MoveRequest{Board: fullBoard, FromStatus: Backlog, FromColumnID: "c-backlog", ToStatus: Ready},
			MoveDecision{ColumnID: "c-ready", Status: Ready, NeedsEndOfColumnOrder: true}},
		{"a same-status move is a reorder: same column, the caller's key, no end-of-column",
			MoveRequest{Board: fullBoard, FromStatus: Review, FromColumnID: "c-review", ToStatus: Review, Order: "a0V"},
			MoveDecision{ColumnID: "c-review", Status: Review, Order: "a0V"}},
		{"a same-status move with no key keeps the task's order",
			MoveRequest{Board: fullBoard, FromStatus: Review, FromColumnID: "c-review", ToColumnID: "c-review"},
			MoveDecision{ColumnID: "c-review", Status: Review}},
		{"a promotion to READY with open blockers lands on BLOCKED",
			MoveRequest{Board: fullBoard, FromStatus: Backlog, FromColumnID: "c-backlog", ToStatus: Ready, OpenBlockers: 1},
			MoveDecision{ColumnID: "c-blocked", Status: Blocked, RedirectedToBlocked: true, NeedsEndOfColumnOrder: true}},
		{"a promotion to IN_PROGRESS with open blockers lands on BLOCKED and takes no WIP slot",
			MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: InProgress, OpenBlockers: 2, InProgressCount: 2},
			MoveDecision{ColumnID: "c-blocked", Status: Blocked, RedirectedToBlocked: true, NeedsEndOfColumnOrder: true}},
		{"a reorder within READY with open blockers is not redirected",
			MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: Ready, OpenBlockers: 1},
			MoveDecision{ColumnID: "c-ready", Status: Ready}},
		{"a move to REVIEW with open blockers is not redirected",
			MoveRequest{Board: fullBoard, FromStatus: InProgress, FromColumnID: "c-prog", ToStatus: Review, OpenBlockers: 1},
			MoveDecision{ColumnID: "c-review", Status: Review, NeedsEndOfColumnOrder: true}},
		{"a board without a BLOCKED column does not redirect",
			MoveRequest{Board: moveBoard, FromStatus: Backlog, FromColumnID: "c-backlog", ToStatus: Ready, OpenBlockers: 1},
			MoveDecision{ColumnID: "c-ready", Status: Ready, NeedsEndOfColumnOrder: true}},
		{"IN_PROGRESS under its limit admits the move",
			MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: InProgress, InProgressCount: 1},
			MoveDecision{ColumnID: "c-prog", Status: InProgress, NeedsEndOfColumnOrder: true}},
		{"a reorder inside a full IN_PROGRESS takes no new slot",
			MoveRequest{Board: fullBoard, FromStatus: InProgress, FromColumnID: "c-prog", ToStatus: InProgress, InProgressCount: 2},
			MoveDecision{ColumnID: "c-prog", Status: InProgress}},
		{"a WIP limit on a column other than IN_PROGRESS is not enforced",
			MoveRequest{Board: fullBoard, FromStatus: Backlog, FromColumnID: "c-backlog", ToStatus: Ready, InProgressCount: 99},
			MoveDecision{ColumnID: "c-ready", Status: Ready, NeedsEndOfColumnOrder: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecideMove(c.req)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("DecideMove = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestDecideMoveRefusals(t *testing.T) {
	t.Run("entering a full IN_PROGRESS is WIP_EXCEEDED", func(t *testing.T) {
		_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: InProgress, InProgressCount: 2})
		wantErrContaining(t, err, CodeWIPExceeded, "Column c-prog WIP limit 2 reached")
	})
	t.Run("an illegal move is INVALID_TRANSITION with the allowed moves and the path", func(t *testing.T) {
		_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Review, FromColumnID: "c-review", ToStatus: Backlog})
		e, ok := AsBatonDeckError(err)
		if !ok || e.Code != CodeInvalidTransition ||
			!reflect.DeepEqual(e.Details["allowed"], []Status{Done, InProgress}) ||
			!reflect.DeepEqual(e.Details["path"], []Status{Review, InProgress, Ready, Backlog}) {
			t.Fatalf("error = %+v", err)
		}
	})
	t.Run("the transition is checked before the WIP limit", func(t *testing.T) {
		_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Backlog, FromColumnID: "c-backlog", ToStatus: InProgress, InProgressCount: 2})
		wantErrContaining(t, err, CodeInvalidTransition, "BACKLOG → IN_PROGRESS")
	})
	for _, s := range []Status{"CANCELLED", "DEAD_LETTER"} {
		t.Run("a move to "+string(s)+" is VALIDATION", func(t *testing.T) {
			_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: s})
			wantErrContaining(t, err, CodeValidation, "unknown status")
		})
	}
	t.Run("a malformed order key is VALIDATION", func(t *testing.T) {
		_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToStatus: Ready, Order: "z"})
		wantErrContaining(t, err, CodeValidation, "Invalid order key")
	})
	t.Run("an unknown target column is NOT_FOUND", func(t *testing.T) {
		_, err := DecideMove(MoveRequest{Board: fullBoard, FromStatus: Ready, FromColumnID: "c-ready", ToColumnID: "nope"})
		wantErrContaining(t, err, CodeNotFound, "Column not found: nope")
	})
}
