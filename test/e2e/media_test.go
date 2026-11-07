//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func init() {
	register(scenario{area: "media", name: "attach-upload-download", tools: []string{"attach_file", "list_attachments"}, run: mediaAttach})
	register(scenario{area: "search", name: "keyword", tools: []string{"search_tasks"}, run: searchKeyword})
	register(scenario{area: "artifacts", name: "add-retract", tools: []string{"add_artifact", "retract_artifact"}, run: artifactsAddRetract})
}

// The worker attaches a file: it is pending until the bytes are PUT to the signed upload URL, then
// ready with a signed download URL that answers the same bytes, and the briefing lists it. A body
// of another size is refused, as is a viewer's attach and a tampered download link.
func mediaAttach(t *testing.T, e *env) {
	p := e.project(t, "Media")
	id := S(e.task(t, p, "with a log", nil), "id")
	body := []byte("2026-11-07 parser: 3 warnings\n")
	att := e.worker.Call(t, "attach_file", M{"projectId": p.id, "taskId": id, "fileName": "parser.log", "mimeType": "text/plain", "kind": "file", "bytes": len(body)})
	aid, up := S(att, "attachmentId"), S(att, "uploadUrl")
	want(t, aid != "" && up != "" && S(att, "expiresAt") != "", "attach_file answered %s", js(att))
	pending := A(e.viewer.Call(t, "list_attachments", M{"projectId": p.id, "taskId": id}), "attachments")
	want(t, len(pending) == 1 && S(pending[0], "id") == aid && S(pending[0], "status") != "ready", "list_attachments before the upload: %s", js(pending))

	code, raw := fetch(t, "PUT", up, body, map[string]string{"Content-Type": "text/plain"})
	want(t, code == 200, "PUT the upload URL: %d %s", code, raw)
	code, raw = fetch(t, "PUT", up, body, map[string]string{"Content-Type": "text/plain"})
	want(t, code == 409, "a second PUT of the same upload answered %d %s, want 409", code, raw)

	ready := A(e.viewer.Call(t, "list_attachments", M{"projectId": p.id, "taskId": id}), "attachments")
	sum := sha256.Sum256(body)
	want(t, len(ready) == 1 && S(ready[0], "status") == "ready" && S(ready[0], "sha256") == hex.EncodeToString(sum[:]) && N(ready[0], "bytes") == float64(len(body)),
		"list_attachments after the upload: %s", js(ready))
	down := S(ready[0], "downloadUrl")
	code, raw = fetch(t, "GET", down, nil, nil)
	want(t, code == 200 && string(raw) == string(body), "GET the download URL: %d %q", code, raw)
	code, _ = fetch(t, "GET", down+"0", nil, nil)
	want(t, code == 403, "a tampered download URL answered %d, want 403", code)
	ctx := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "include": []any{"attachments"}})
	want(t, has(ids(A(ctx, "attachments"), "id"), aid), "get_task_context does not list the attachment: %s", js(A(ctx, "attachments")))

	short := e.worker.Call(t, "attach_file", M{"projectId": p.id, "taskId": id, "fileName": "short.log", "mimeType": "text/plain", "kind": "file", "bytes": 100})
	code, raw = fetch(t, "PUT", S(short, "uploadUrl"), body, nil)
	want(t, code == 400, "a body of another size answered %d %s, want 400", code, raw)

	e.viewer.Refuse(t, "attach_file", M{"projectId": p.id, "taskId": id, "fileName": "x.txt", "mimeType": "text/plain", "kind": "file", "bytes": 1}, "FORBIDDEN")
	e.worker.Refuse(t, "attach_file", M{"projectId": p.id, "taskId": id, "fileName": "x.bin", "mimeType": "application/octet-stream", "kind": "hologram", "bytes": 1}, "VALIDATION")
	e.outsider.Refuse(t, "list_attachments", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
}

// search_tasks finds by keyword over title and description, every term must match, word* is a
// prefix, and k caps the results; hybrid answers the keyword results. semantic is refused and an
// outsider may not search.
func searchKeyword(t *testing.T, e *env) {
	p := e.project(t, "Search")
	lexer := S(e.task(t, p, "Tokenizer for the feed", M{"description": "Split the feed into tokens."}), "id")
	parser := S(e.task(t, p, "Parser for the feed", M{"description": "Build the tree from tokens."}), "id")
	e.task(t, p, "Renderer", M{"description": "Draw the board."})

	find := func(args M) []string {
		args["projectId"] = p.id
		out := e.viewer.Call(t, "search_tasks", args)
		var got []string
		for _, r := range A(out, "results") {
			got = append(got, S(r, "task.id"))
		}
		return got
	}
	both := find(M{"query": "feed tokens"})
	want(t, len(both) == 2 && has(both, lexer) && has(both, parser), "search 'feed tokens': %v", both)
	want(t, js(find(M{"query": "pars*"})) == js([]string{parser}), "search 'pars*': %v", find(M{"query": "pars*"}))
	want(t, js(find(M{"query": "feed tree"})) == js([]string{parser}), "every term must match: 'feed tree' answered %v", find(M{"query": "feed tree"}))
	want(t, len(find(M{"query": "feed", "k": 1})) == 1, "k 1 answered more than one result")
	want(t, len(find(M{"query": "nothingmatchesthis"})) == 0, "a query matching nothing answered results")
	want(t, js(find(M{"query": "renderer", "boardId": p.board, "mode": "hybrid"})) != "null", "hybrid search answered nothing")

	e.viewer.Refuse(t, "search_tasks", M{"projectId": p.id, "query": "feed", "mode": "semantic"}, "VALIDATION")
	e.outsider.Refuse(t, "search_tasks", M{"projectId": p.id, "query": "feed"}, "FORBIDDEN")
}

// The worker binds a pr and a branch to a task (a retry adds nothing), the master retracts the pr
// with a reason, and the briefing marks it retracted. Retracting what is not there is NOT_FOUND, an
// artifact without a url or a ref is refused, and the viewer may do neither.
func artifactsAddRetract(t *testing.T, e *env) {
	p := e.project(t, "Artifacts")
	id := S(e.task(t, p, "shipped", nil), "id")
	arts := []any{M{"kind": "pr", "url": "https://example.com/acme/app/pull/7"}, M{"kind": "branch", "ref": "feat/parser", "repo": "acme/app"}}
	got := e.worker.Call(t, "add_artifact", M{"projectId": p.id, "taskId": id, "artifacts": arts})
	want(t, N(got, "added") == 2 && len(A(got, "task.artifacts")) == 2, "add_artifact answered %s", js(got))
	again := e.worker.Call(t, "add_artifact", M{"projectId": p.id, "taskId": id, "artifacts": arts})
	want(t, N(again, "added") == 0, "a retried add_artifact added %v", N(again, "added"))
	want(t, len(A(e.get(t, p, id), "artifacts")) == 2, "get_task artifacts: %s", js(A(e.get(t, p, id), "artifacts")))

	e.viewer.Refuse(t, "add_artifact", M{"projectId": p.id, "taskId": id, "artifacts": []any{M{"kind": "doc", "url": "https://example.com/d"}}}, "FORBIDDEN")
	e.worker.Refuse(t, "add_artifact", M{"projectId": p.id, "taskId": id, "artifacts": []any{M{"kind": "pr"}}}, "VALIDATION")

	ret := e.master.Call(t, "retract_artifact", M{"projectId": p.id, "taskId": id, "artifact": M{"kind": "pr", "url": "https://example.com/acme/app/pull/7"}, "reason": "wrong PR"})
	want(t, N(ret, "retracted") == 1, "retract_artifact answered %s", js(ret))
	ctx := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id})
	state := map[string]bool{}
	for _, a := range A(ctx, "artifacts") {
		state[S(a, "kind")] = S(a, "retracted.reason") == "wrong PR" && S(a, "retracted.by") == e.srv.ids["master"]
	}
	want(t, state["pr"] && !state["branch"], "get_task_context artifacts after the retraction: %s", js(A(ctx, "artifacts")))
	e.master.Refuse(t, "retract_artifact", M{"projectId": p.id, "taskId": id, "artifact": M{"kind": "pr", "url": "https://example.com/none"}, "reason": "x"}, "NOT_FOUND")
	e.viewer.Refuse(t, "retract_artifact", M{"projectId": p.id, "taskId": id, "artifact": M{"kind": "branch", "ref": "feat/parser"}, "reason": "x"}, "FORBIDDEN")
}
