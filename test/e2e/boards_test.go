//go:build e2e

package e2e

import (
	"testing"
)

func init() {
	register(scenario{area: "boards", name: "create-list-get", tools: []string{"create_board", "list_boards", "get_board"}, run: boardCreateListGet})
	register(scenario{area: "boards", name: "columns", tools: []string{"add_column", "update_column", "remove_column"}, run: boardColumns})
}

// fixedTable is the transition table every board reports (ADR-0005).
var fixedTable = map[string][]string{
	"TODO":        {"IN_PROGRESS"},
	"IN_PROGRESS": {"REVIEW", "BLOCKED", "TODO"},
	"BLOCKED":     {"IN_PROGRESS", "TODO"},
	"REVIEW":      {"DONE", "IN_PROGRESS"},
	"DONE":        {"IN_PROGRESS"},
}

// A worker makes a board with its own columns and one with the defaults; both are listed, and
// get_board answers the columns, the per-column task counts and the fixed transition table. A viewer
// cannot make a board, a nameless board is refused, and an outsider cannot read one.
func boardCreateListGet(t *testing.T, e *env) {
	p := e.project(t, "Boards")
	custom := O(e.worker.Call(t, "create_board", M{"projectId": p.id, "name": "Ops", "idempotencyKey": uniq("b"), "columns": []any{
		M{"name": "Inbox", "status": "TODO"},
		M{"name": "Doing", "status": "IN_PROGRESS", "wipLimit": 2},
		M{"name": "Shipped", "status": "DONE"},
	}}), "board")
	cid := S(custom, "id")
	want(t, len(A(custom, "columns")) == 3, "create_board with columns answered %s", js(custom))

	listed := ids(A(e.viewer.Call(t, "list_boards", M{"projectId": p.id}), "boards"), "id")
	want(t, has(listed, cid) && has(listed, p.board), "list_boards as the viewer: %v, want %s and %s", listed, cid, p.board)

	e.task(t, p, "on main", nil)
	e.task(t, p, "also on main", nil)
	got := e.viewer.Call(t, "get_board", M{"projectId": p.id, "boardId": p.board})
	want(t, N(got, "taskCounts."+p.cols["TODO"]) == 2, "get_board taskCounts: %s, want 2 in the TODO column %s", js(O(got, "taskCounts")), p.cols["TODO"])
	statuses := []string{}
	for _, c := range A(got, "board.columns") {
		statuses = append(statuses, S(c, "status"))
	}
	want(t, js(statuses) == `["TODO","IN_PROGRESS","BLOCKED","REVIEW","DONE"]`, "the default columns map %v", statuses)
	want(t, js(O(got, "board.transitions")) == js(fixedTable), "get_board transitions %s, want the fixed table", js(O(got, "board.transitions")))
	ops := e.viewer.Call(t, "get_board", M{"projectId": p.id, "boardId": cid})
	want(t, N(ops, "board.columns.1.wipLimit") == 2, "get_board of the custom board: %s", js(ops))

	// With review off, the default columns leave Review out.
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	noReview := O(e.worker.Call(t, "create_board", M{"projectId": p.id, "name": "Plain"}), "board")
	for _, c := range A(noReview, "columns") {
		want(t, S(c, "status") != "REVIEW", "a board made with review off has a Review column: %s", js(noReview))
	}

	e.viewer.Refuse(t, "create_board", M{"projectId": p.id, "name": "Nope"}, "FORBIDDEN")
	e.worker.Refuse(t, "create_board", M{"projectId": p.id, "name": ""}, "VALIDATION")
	e.outsider.Refuse(t, "get_board", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
	e.outsider.Refuse(t, "list_boards", M{"projectId": p.id}, "FORBIDDEN")
	e.viewer.Refuse(t, "get_board", M{"projectId": p.id, "boardId": "B-nosuchboard"}, "NOT_FOUND")
}

// A worker adds a column; the project admin (a structural edit) renames it, re-maps it and sets a
// WIP limit, which then refuses a claim past it; an empty column is removed and a column holding a
// task is not. The master cannot make structural edits.
func boardColumns(t *testing.T, e *env) {
	p := e.project(t, "Columns")
	col := O(e.worker.Call(t, "add_column", M{"projectId": p.id, "boardId": p.board, "name": "QA", "status": "REVIEW"}), "column")
	qa := S(col, "id")
	want(t, S(col, "status") == "REVIEW" && S(col, "name") == "QA", "add_column answered %s", js(col))
	columns := func() []any {
		return A(e.viewer.Call(t, "get_board", M{"projectId": p.id, "boardId": p.board}), "board.columns")
	}
	want(t, has(ids(columns(), "id"), qa), "get_board does not list the added column %s", qa)
	e.viewer.Refuse(t, "add_column", M{"projectId": p.id, "boardId": p.board, "name": "X", "status": "TODO"}, "FORBIDDEN")
	e.worker.Refuse(t, "add_column", M{"projectId": p.id, "boardId": p.board, "name": "X", "status": "CANCELLED"}, "VALIDATION")

	e.master.Refuse(t, "update_column", M{"projectId": p.id, "boardId": p.board, "columnId": qa, "name": "QA two"}, "FORBIDDEN")
	upd := O(e.admin.Call(t, "update_column", M{"projectId": p.id, "boardId": p.board, "columnId": qa, "name": "Verify", "status": "DONE"}), "board")
	for _, c := range A(upd, "columns") {
		if S(c, "id") == qa {
			want(t, S(c, "name") == "Verify" && S(c, "status") == "DONE", "update_column answered %s", js(c))
		}
	}
	// A WIP limit of 1 on the IN_PROGRESS column: the second claim is refused.
	e.admin.Call(t, "update_column", M{"projectId": p.id, "boardId": p.board, "columnId": p.cols["IN_PROGRESS"], "wipLimit": 1})
	for _, c := range columns() {
		if S(c, "id") == p.cols["IN_PROGRESS"] {
			want(t, N(c, "wipLimit") == 1, "get_board after update_column wipLimit: %s", js(c))
		}
	}
	a, b := S(e.task(t, p, "first", nil), "id"), S(e.task(t, p, "second", nil), "id")
	claim(t, e.worker, p, a)
	e.worker2.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": b}, "WIP_EXCEEDED")

	e.master.Refuse(t, "remove_column", M{"projectId": p.id, "boardId": p.board, "columnId": qa}, "FORBIDDEN")
	e.admin.Refuse(t, "remove_column", M{"projectId": p.id, "boardId": p.board, "columnId": p.cols["IN_PROGRESS"]}, "VALIDATION")
	rem := O(e.admin.Call(t, "remove_column", M{"projectId": p.id, "boardId": p.board, "columnId": qa}), "board")
	want(t, !has(ids(A(rem, "columns"), "id"), qa), "remove_column answered a board still holding %s", qa)
	want(t, !has(ids(columns(), "id"), qa), "get_board still lists the removed column %s", qa)
}
