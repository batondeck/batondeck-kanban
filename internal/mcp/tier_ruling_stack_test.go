package mcp_test

import "testing"

// TestWorkerCannotCreateBoardsAddColumnsOrDeletePages: the owner's ruling of 2026-11-10 puts
// create_board, add_column and delete_chronicle_page at tier master, on every engine. A worker is
// refused FORBIDDEN naming the master role and writes nothing; the same member promoted to master
// does all three.
func TestWorkerCannotCreateBoardsAddColumnsOrDeletePages(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		board := obj(r.ada.call("create_board", map[string]any{"projectId": r.p, "name": "Main"})["board"])
		boardID := str(board["id"])
		page := str(r.ada.call("create_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-keep", "title": "Keep", "blocks": cblocks("a", "kept")})["pageId"])
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "worker"})

		boards := func() int { return len(r.ada.call("list_boards", map[string]any{"projectId": r.p})["boards"].([]any)) }
		columns := func() int {
			b := obj(r.ada.call("get_board", map[string]any{"projectId": r.p, "boardId": boardID})["board"])
			return len(b["columns"].([]any))
		}
		nBoards, nColumns := boards(), columns()

		// A low order and a WIP limit of 1: the column that would redirect every claim and then refuse them.
		trap := map[string]any{"projectId": r.p, "boardId": boardID, "name": "Trap", "status": "TODO", "wipLimit": 1, "order": 0}
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"create_board", map[string]any{"projectId": r.p, "name": "Grace's"}},
			{"add_column", trap},
			{"delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page}},
		} {
			e := r.wantCode(r.grace, c.tool, c.args, "FORBIDDEN")
			if d := obj(e["details"]); d["requiredRole"] != "master" || d["role"] != "worker" {
				t.Fatalf("%s as a worker: %v, want requiredRole master", c.tool, e)
			}
		}
		var view struct {
			Page *struct{ ID string } `json:"page"`
		}
		r.read(r.ada, r.uri(r.p, "/"+page), &view)
		if boards() != nBoards || columns() != nColumns || view.Page == nil {
			t.Fatalf("the refused calls wrote: %d boards (was %d), %d columns (was %d), page %+v", boards(), nBoards, columns(), nColumns, view.Page)
		}

		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "master"})
		if str(obj(r.grace.call("create_board", map[string]any{"projectId": r.p, "name": "Grace's"})["board"])["id"]) == "" {
			t.Fatal("create_board as a master answered no board")
		}
		if str(obj(r.grace.call("add_column", trap)["column"])["id"]) == "" {
			t.Fatal("add_column as a master answered no column")
		}
		if got := r.grace.call("delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page}); got["ok"] != true {
			t.Fatalf("delete_chronicle_page as a master = %v", got)
		}
	})
}
