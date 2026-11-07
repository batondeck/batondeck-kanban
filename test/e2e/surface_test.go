//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func init() {
	register(scenario{area: "prompts", name: "render-and-follow", prompts: []string{"pick_up_next_task", "triage_inbox", "summarize_for_handoff", "decompose_into_subtasks"}, run: promptsRenderAndFollow})
	register(scenario{area: "resources", name: "board-and-task", templates: []string{"board", "task", "task-context"}, run: resourcesBoardAndTask})
	register(scenario{area: "resources", name: "feed", templates: []string{"board-feed", "board-feed-cursor"}, run: resourcesFeed})
	register(scenario{area: "resources", name: "chronicle", templates: []string{"chronicle", "chronicle-cursor", "chronicle-page", "chronicle-history", "chronicle-history-cursor", "chronicle-version"}, run: resourcesChronicle})
	register(scenario{area: "transport", name: "streamable-http", run: transportStreamableHTTP})
}

var snake = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)

// toolsNamed is every tool name the text names, of the live list.
func toolsNamed(text string, live []string) []string {
	var out []string
	for _, w := range snake.FindAllString(text, -1) {
		if has(live, w) && !has(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Each prompt renders one user message naming the real project and board or task, and every tool
// it tells the agent to call is a live tool; a blank argument is invalid params. The worker then
// follows pick_up_next_task and summarize_for_handoff, and the master triage_inbox and
// decompose_into_subtasks, through the tools they name, and each effect is read back.
func promptsRenderAndFollow(t *testing.T, e *env) {
	p := e.project(t, "Prompts")
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	live := e.admin.List(t, "tools/list", "tools", "name")
	id := S(e.task(t, p, "Parse the feed", M{"priority": "high"}), "id")

	render := func(c *Client, name string, args map[string]string, wantTools ...string) string {
		text, rerr := c.Prompt(t, name, args)
		want(t, rerr == nil, "prompts/get %s: %v", name, rerr)
		for _, v := range args {
			want(t, strings.Contains(text, v), "prompt %s does not name %s: %q", name, v, text)
		}
		named := toolsNamed(text, live)
		for _, w := range wantTools {
			want(t, has(named, w), "prompt %s does not name the live tool %s (it names %v)", name, w, named)
		}
		for _, w := range snake.FindAllString(text, -1) {
			if strings.HasSuffix(w, "_task") || strings.HasSuffix(w, "_tasks") || strings.HasPrefix(w, "add_") || strings.HasPrefix(w, "get_") || strings.HasPrefix(w, "set_") {
				want(t, has(live, w), "prompt %s names %s, which is not a live tool", name, w)
			}
		}
		return text
	}
	board := map[string]string{"projectId": p.id, "boardId": p.board}
	task := map[string]string{"projectId": p.id, "taskId": id}

	// pick_up_next_task, followed: next_task, claim_task, get_task_context, complete_task.
	render(e.worker, "pick_up_next_task", board, "next_task", "claim_task", "get_task_context", "heartbeat_task", "complete_task", "block_task", "handoff_task")
	next := e.worker.Call(t, "next_task", M{"projectId": p.id, "boardId": p.board})
	want(t, S(next, "task.id") == id, "following pick_up_next_task: next_task answered %s", js(next))
	lease := claim(t, e.worker, p, id)
	e.worker.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "includeUpstream": true})

	// summarize_for_handoff, followed: a digest stored with set_summary.
	render(e.worker, "summarize_for_handoff", task, "get_task_context", "set_summary")
	e.worker.Call(t, "set_summary", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "summary": "Tokenizer done; parser remains."})
	e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "deliverable": "parser"})
	got := e.get(t, p, id)
	want(t, S(got, "status") == "DONE" && S(got, "contextSummary") == "Tokenizer done; parser remains.", "after following the worker prompts: %s", js(got))

	// triage_inbox, followed: list the TODO tasks and give each a modelHint.
	render(e.master, "triage_inbox", board, "list_tasks", "update_task", "add_context_item", "add_dependency", "delete_task")
	inbox := S(e.task(t, p, "Render the board", nil), "id")
	todo := ids(A(e.master.Call(t, "list_tasks", M{"projectId": p.id, "boardId": p.board, "status": "TODO"}), "tasks"), "id")
	want(t, js(todo) == js([]string{inbox}), "following triage_inbox: list_tasks TODO answered %v", todo)
	e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": inbox, "version": e.version(t, p, inbox), "patch": M{"modelHint": M{"model": "sonnet", "effort": "medium", "rationale": "routine UI"}}})
	want(t, S(e.get(t, p, inbox), "modelHint.model") == "sonnet", "the triaged task has no modelHint")

	// decompose_into_subtasks, followed: two subtasks, one blocking the other.
	render(e.master, "decompose_into_subtasks", map[string]string{"projectId": p.id, "taskId": inbox}, "add_subtask", "add_dependency", "set_summary")
	a := S(O(e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": inbox, "title": "Lay out the columns"}), "task"), "id")
	b := S(O(e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": inbox, "title": "Draw the cards"}), "task"), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": a, "toTaskId": b})
	want(t, has(strs(A(e.get(t, p, b), "blockedBy")), a), "following decompose_into_subtasks: the tree is not wired")

	for _, name := range []string{"pick_up_next_task", "triage_inbox"} {
		_, rerr := e.worker.Prompt(t, name, map[string]string{"projectId": p.id, "boardId": "  "})
		want(t, rerr != nil && rerr.Code == -32602, "prompts/get %s with a blank boardId answered %v, want invalid params", name, rerr)
	}
	for _, name := range []string{"summarize_for_handoff", "decompose_into_subtasks"} {
		_, rerr := e.worker.Prompt(t, name, map[string]string{"projectId": p.id})
		want(t, rerr != nil && rerr.Code == -32602, "prompts/get %s without taskId answered %v, want invalid params", name, rerr)
	}
	_, rerr := e.worker.Prompt(t, "no_such_prompt", map[string]string{"projectId": p.id})
	want(t, rerr != nil, "prompts/get of an unknown prompt answered a prompt")
}

// refusal is the BatonDeckError code a refused resource read carries in its JSON-RPC data.
func refusal(rerr *RPCError) string {
	if rerr == nil {
		return ""
	}
	var d M
	_ = json.Unmarshal(rerr.Data, &d)
	if c := S(d, "code"); c != "" {
		return c
	}
	return S(d, "error.code")
}

// The board resource answers the columns and per-column counts get_board answers; the task and
// task-context resources answer what get_task and get_task_context do, after a write. An unknown
// task is NOT_FOUND, an outsider is FORBIDDEN, and an unlisted shape is not found.
func resourcesBoardAndTask(t *testing.T, e *env) {
	p := e.project(t, "Resources")
	id := S(e.task(t, p, "Read me", nil), "id")
	e.task(t, p, "And me", nil)
	e.worker.Call(t, "set_summary", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "summary": "Halfway."})
	e.worker.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "decision", "body": "Use UTF-8."})

	board, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/board/"+p.board)
	want(t, rerr == nil, "reading the board resource: %v", rerr)
	tool := e.viewer.Call(t, "get_board", M{"projectId": p.id, "boardId": p.board})
	want(t, js(O(board, "taskCounts")) == js(O(tool, "taskCounts")) && N(board, "taskCounts."+p.cols["TODO"]) == 2 &&
		js(A(board, "board.columns")) == js(A(tool, "board.columns")), "the board resource %s, get_board %s", js(board), js(tool))

	res, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/task/"+id)
	want(t, rerr == nil && S(res, "task.id") == id && S(res, "task.contextSummary") == "Halfway." && N(res, "task.version") == e.version(t, p, id),
		"the task resource: %v %s", rerr, js(res))
	ctx, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/task/"+id+"/context")
	want(t, rerr == nil && S(ctx, "summary") == "Halfway." && S(ctx, "items.0.body") == "Use UTF-8." && O(ctx, "workflow") != nil,
		"the task-context resource: %v %s", rerr, js(ctx))

	_, rerr = e.viewer.Read(t, "batondeck://"+p.id+"/task/T-nosuchtask")
	want(t, rerr != nil && rerr.Code == -32602 && refusal(rerr) == "NOT_FOUND", "an unknown task resource answered %v (%s)", rerr, refusal(rerr))
	_, rerr = e.outsider.Read(t, "batondeck://"+p.id+"/board/"+p.board)
	want(t, rerr != nil && refusal(rerr) == "FORBIDDEN", "the outsider's board read answered %v (%s)", rerr, refusal(rerr))
	_, rerr = e.outsider.Read(t, "batondeck://"+p.id+"/task/"+id+"/context")
	want(t, rerr != nil && refusal(rerr) == "FORBIDDEN", "the outsider's task-context read answered %v (%s)", rerr, refusal(rerr))
	rep, err := e.viewer.Request("resources/read", M{"uri": "batondeck://" + p.id + "/nothing/here"})
	want(t, err == nil && rep.Err != nil, "an unlisted resource shape answered %v %v", err, rep)
}

// The feed resource answers the board's events newest first, 50 a page; its cursor twin answers
// the next, older page from the nextCursor, with no event twice. A forged cursor is VALIDATION.
func resourcesFeed(t *testing.T, e *env) {
	p := e.project(t, "Feed")
	var made []string
	for i := 0; i < 52; i++ {
		made = append(made, S(e.task(t, p, fmt.Sprintf("event %02d", i), nil), "id"))
	}
	last := made[len(made)-1]
	claim(t, e.worker, p, last)

	types := e.feedTypes(t, e.viewer, p)
	want(t, len(types) == 50 && types[0] == "task.claimed" && types[1] == "task.created", "the feed's first page types: %v", types)
	first, _ := e.viewer.Read(t, "batondeck://"+p.id+"/board/"+p.board+"/feed")
	want(t, S(first, "events.0.taskId") == last && S(first, "events.0.actor") == e.srv.ids["worker"] && S(first, "nextCursor") != "", "the feed's first page: %s", js(A(first, "events")[:2]))
	cursor := S(first, "nextCursor")
	older, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/board/"+p.board+"/feed?cursor="+cursor)
	want(t, rerr == nil && len(A(older, "events")) > 0, "the feed's cursor page: %v %s", rerr, js(older))
	seen := map[string]bool{}
	for _, ev := range A(first, "events") {
		seen[S(ev, "id")] = true
	}
	for _, ev := range A(older, "events") {
		want(t, !seen[S(ev, "id")], "event %s is on both pages", S(ev, "id"))
		want(t, S(ev, "ts") <= S(A(first, "events")[49], "ts"), "the cursor page holds a newer event: %s", js(ev))
	}
	// Every task.created is on one of the two pages (plus project and board setup events).
	created := 0
	for _, ev := range append(A(first, "events"), A(older, "events")...) {
		if S(ev, "type") == "task.created" {
			created++
		}
	}
	want(t, created == 52, "the two feed pages hold %d task.created events, want 52", created)
	_, rerr = e.viewer.Read(t, "batondeck://"+p.id+"/board/"+p.board+"/feed?cursor=not-a-cursor")
	want(t, rerr != nil && refusal(rerr) == "VALIDATION", "a forged feed cursor answered %v (%s)", rerr, refusal(rerr))
	_, rerr = e.outsider.Read(t, "batondeck://"+p.id+"/board/"+p.board+"/feed")
	want(t, rerr != nil && refusal(rerr) == "FORBIDDEN", "the outsider's feed read answered %v (%s)", rerr, refusal(rerr))
}

// The chronicle index lists 100 pages by slug and its cursor twin the rest; a page reads with its
// head, its history newest first (50 a page, the cursor twin the rest) and each version by id with
// its blocks. An outsider reads none of it and a missing version reads as null.
func resourcesChronicle(t *testing.T, e *env) {
	p := e.project(t, "Chronicle reads")
	var pages []string
	for i := 0; i < 101; i++ {
		out := e.worker.Call(t, "create_chronicle_page", M{"projectId": p.id, "kind": "topic", "slug": fmt.Sprintf("topic/t-%03d", i), "title": fmt.Sprintf("Topic %d", i), "blocks": blocks("body", "text")})
		pages = append(pages, S(out, "pageId"))
	}
	idx, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle")
	want(t, rerr == nil && len(A(idx, "pages")) == 100 && S(idx, "pages.0.slug") == "topic/t-000" && S(idx, "nextCursor") != "", "the chronicle index: %v %d pages", rerr, len(A(idx, "pages")))
	rest, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle?cursor="+S(idx, "nextCursor"))
	want(t, rerr == nil && len(A(rest, "pages")) == 1 && S(rest, "pages.0.slug") == "topic/t-100" && S(rest, "nextCursor") == "", "the chronicle index's cursor page: %v %s", rerr, js(rest))

	pid := pages[0]
	head := ""
	versions := []string{}
	for i := 0; i < 51; i++ {
		got, _ := page(t, e.viewer, p.id, pid)
		head = S(got, "page.headVersionId")
		if i == 0 {
			versions = append(versions, head)
		}
		out := e.worker.Call(t, "edit_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedParentVersionId": head, "blocks": blocks("body", fmt.Sprintf("revision %d", i+1))})
		versions = append(versions, S(out, "versionId"))
	}
	got, texts := page(t, e.viewer, p.id, pid)
	want(t, S(got, "page.headVersionId") == versions[51] && texts["body"] == "revision 51", "the chronicle page after 51 edits: %s", js(O(got, "page")))

	hist, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions")
	want(t, rerr == nil && len(A(hist, "versions")) == 50 && S(hist, "versions.0.id") == versions[51] && S(hist, "nextCursor") != "", "the history's first page: %v %d versions", rerr, len(A(hist, "versions")))
	older, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions?cursor="+S(hist, "nextCursor"))
	want(t, rerr == nil && js(ids(A(older, "versions"), "id")) == js([]string{versions[1], versions[0]}), "the history's cursor page: %v %v, want %v", rerr, ids(A(older, "versions"), "id"), []string{versions[1], versions[0]})

	v, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions/"+versions[10])
	want(t, rerr == nil && S(v, "version.id") == versions[10] && S(v, "version.parentVersionId") == versions[9] && S(v, "version.blocks.0.text") == "revision 10",
		"the chronicle version resource: %v %s", rerr, js(v))
	missing, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions/V-nosuch")
	want(t, rerr == nil && missing["version"] == nil, "a missing version answered %v %s", rerr, js(missing))

	for _, uri := range []string{"/chronicle", "/chronicle/" + pid, "/chronicle/" + pid + "/versions", "/chronicle/" + pid + "/versions/" + versions[0]} {
		_, rerr := e.outsider.Read(t, "batondeck://"+p.id+uri)
		want(t, rerr != nil && refusal(rerr) == "FORBIDDEN", "the outsider's read of %s answered %v (%s)", uri, rerr, refusal(rerr))
	}
	_, rerr = e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions?cursor=forged")
	want(t, rerr != nil && refusal(rerr) == "VALIDATION", "a forged history cursor answered %v (%s)", rerr, refusal(rerr))
	_, rerr = e.viewer.Read(t, "batondeck://"+p.id+"/chronicle?cursor=forged")
	want(t, rerr != nil && refusal(rerr) == "VALIDATION", "a forged index cursor answered %v (%s)", rerr, refusal(rerr))
}

// raw posts body to /mcp with headers and answers the status, content type and body.
func raw(t *testing.T, s *Server, headers map[string]string, body string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest("POST", s.base+"/mcp", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

// as is a client on c's server sending token and session, for requests no real client sends.
func as(c *Client, token, session string) *Client {
	return &Client{srv: c.srv, url: c.url, token: token, name: c.name, session: session, proto: c.proto, http: c.http}
}

// The transport, as a client meets it: initialize answers the session header and SSE replies; a
// refused initialize is a JSON reply; a request without a bearer is 401 with a challenge, one
// without both media types in Accept is refused, an unknown session is 404, another identity's
// bearer on a session is 403, and a closed session is gone.
func transportStreamableHTTP(t *testing.T, e *env) {
	s := e.srv
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`
	both := "application/json, text/event-stream"

	code, ctype, body := raw(t, s, map[string]string{"Content-Type": "application/json", "Accept": both}, initReq)
	want(t, code == 401, "initialize without a bearer answered %d %s %q", code, ctype, body)
	req, _ := http.NewRequest("POST", s.base+"/mcp", bytes.NewBufferString(initReq))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", both)
	res, err := http.DefaultClient.Do(req)
	want(t, err == nil && res.StatusCode == 401 && strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer"), "the 401 carries no Bearer challenge: %v", res.Header)
	res.Body.Close()

	auth := "Bearer " + s.tokens["viewer"]
	code, ctype, body = raw(t, s, map[string]string{"Authorization": auth, "Content-Type": "application/json", "Accept": "application/json"}, initReq)
	want(t, code == 400 || code == 406, "initialize accepting only JSON answered %d %s %q", code, ctype, body)

	// The JSON reply: an initialize under a protocol version header the server does not speak.
	code, ctype, body = raw(t, s, map[string]string{"Authorization": auth, "Content-Type": "application/json", "Accept": both, "MCP-Protocol-Version": "2026-07-28"}, initReq)
	var rep Reply
	err = decodeResponse([]byte(body), 1, &rep)
	want(t, code == 400 && strings.HasPrefix(ctype, "application/json") && err == nil && rep.Err != nil, "initialize with protocol version 2026-07-28 answered %d %s %q (%v)", code, ctype, body, err)

	// A session: SSE replies, bound to its identity.
	c := s.connect(t, s.tokens["viewer"], "e2e-transport")
	want(t, c.session != "" && c.proto != "" && c.ServerVersion == binVersion && c.Instructions != "", "initialize answered session %q proto %q version %q (the binary reports %q)", c.session, c.proto, c.ServerVersion, binVersion)
	r, err := c.Request("tools/list", M{})
	want(t, err == nil && r.ContentType == "text/event-stream", "tools/list answered %v %v", r, err)

	other := as(c, s.tokens["worker2"], c.session)
	_, err = other.Request("tools/list", M{})
	var he *HTTPError
	want(t, errors.As(err, &he) && he.Status == 403, "another identity's bearer on the session answered %v, want 403", err)

	stranger := as(c, c.token, "not-a-session")
	_, err = stranger.Request("tools/list", M{})
	want(t, errors.As(err, &he) && he.Status == 404, "an unknown session answered %v, want 404", err)

	c.Close()
	_, err = c.Request("tools/list", M{})
	want(t, errors.As(err, &he) && he.Status == 404, "a closed session answered %v, want 404", err)
	c.session = ""
}
