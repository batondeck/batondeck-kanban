package mcp_test

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The real-stack tests: the same harness and the same registry over the real service on the
// engines servicetest.Engines selects (BATONDECK_TEST_ENGINE), where the mapping tests above run
// over servicefake (ADR-0003 B3, the join).

const (
	stackOwner = "bk_stack_ada" // creates every project, so its admin
	stackGrace = "bk_stack_grace"
	stackRoot  = "bk_stack_root" // u-ada again, as a workspace admin: the on-call tools are admin-only
)

func stackResolver() authfake.Resolver {
	return authfake.Resolver{Callers: map[string]domain.Caller{
		stackOwner: {Identity: "u-ada", Agent: "agent-ada", TokenScope: "u-ada"},
		stackGrace: {Identity: "u-grace", TokenScope: "u-grace"},
		stackRoot:  {Identity: "u-ada", Agent: "agent-ada", TokenScope: "u-ada", WorkspaceAdmin: true},
	}}
}

// bootStack is the harness with every registered area's tools over the stack's real services, its
// tools/list narrowed by the real grants as the binary's is.
func bootStack(t *testing.T, s *servicetest.Stack) *bk.Harness {
	t.Helper()
	h := bk.BootHarness(t, s.Services, stackResolver(), func(o *bk.Options) { o.HiddenTools = s.Core.HiddenTools })
	tools.Register(h.Server(), s.Services)
	s.Core.SetAgentSessionEnder(h.Server())
	return h
}

// caller is one session's tool calls, each recorded by name.
type caller struct {
	t      *testing.T
	cs     *sdk.ClientSession
	called map[string]int
}

func (c *caller) call(tool string, args map[string]any) map[string]any {
	c.t.Helper()
	res, err := c.cs.CallTool(testCtx(c.t), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		c.t.Fatalf("%s %v: %v", tool, args, err)
	}
	if res.IsError {
		c.t.Fatalf("%s %v: %s", tool, args, text(res))
	}
	c.called[tool]++
	out, _ := res.StructuredContent.(map[string]any)
	return out
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func str(v any) string         { s, _ := v.(string); return s }

// TestEveryToolOnTheRealStack drives every registered tool once, in a scenario a client could run,
// through a real client against the real service, on every selected engine. Each call answers
// without isError, so each real response passed the server's own output-schema validation (a
// response the schema refuses is INTERNAL, TestBadOutputIsInternal); a tool registered without a
// call here fails the test, so a new area joins the real stack when it lands. The resource
// templates are read over the same data, the cursor twins with a real cursor.
func TestEveryToolOnTheRealStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		c := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}

		p := str(c.call("create_project", map[string]any{"name": "Stack", "idempotencyKey": "p-1"})["projectId"])
		if got := str(obj(c.call("get_project", map[string]any{"projectId": p})["project"])["id"]); got != p {
			t.Fatalf("get_project answered %q, want %q", got, p)
		}
		if n := len(c.call("list_projects", map[string]any{})["projects"].([]any)); n != 1 {
			t.Fatalf("list_projects: %d projects, want 1", n)
		}
		c.call("update_settings", map[string]any{"projectId": p, "maxRunsPerTask": 3})
		c.call("add_member", map[string]any{"projectId": p, "identityId": "u-grace", "role": "worker"})

		board := obj(c.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])
		b := str(board["id"])
		if n := len(c.call("list_boards", map[string]any{"projectId": p})["boards"].([]any)); n != 1 {
			t.Fatalf("list_boards: %d boards, want 1", n)
		}
		got := obj(obj(c.call("get_board", map[string]any{"projectId": p, "boardId": b})["board"])["transitions"])
		if want := asJSON(t, fixedTable()); !jsonEqual(got, want) || len(got) != len(domain.AllStatuses()) {
			t.Fatalf("get_board transitions %v, want the fixed table %v", got, want)
		}
		col := str(obj(c.call("add_column", map[string]any{"projectId": p, "boardId": b, "name": "QA", "status": "REVIEW"})["column"])["id"])
		c.call("update_column", map[string]any{"projectId": p, "boardId": b, "columnId": col, "name": "QA two"})
		c.call("remove_column", map[string]any{"projectId": p, "boardId": b, "columnId": col})

		task := func(title string) map[string]any {
			return obj(c.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": title, "description": "the stack " + title})["task"])
		}
		get := func(id string) map[string]any {
			return obj(c.call("get_task", map[string]any{"projectId": p, "taskId": id})["task"])
		}
		t1, t2, t3 := task("one"), task("two"), task("three")
		id1, id2, id3 := str(t1["id"]), str(t2["id"]), str(t3["id"])
		if t1["status"] != "TODO" || id1 == id2 {
			t.Fatalf("create_task answered %v and %v, want two TODO tasks", t1, t2)
		}
		c.call("update_task", map[string]any{"projectId": p, "taskId": id1, "version": get(id1)["version"], "patch": map[string]any{"title": "one renamed"}})
		if n := len(c.call("list_tasks", map[string]any{"projectId": p, "boardId": b})["tasks"].([]any)); n != 3 {
			t.Fatalf("list_tasks: %d tasks, want 3", n)
		}

		edge := str(obj(c.call("add_dependency", map[string]any{"projectId": p, "fromTaskId": id1, "toTaskId": id2})["edge"])["id"])
		c.call("remove_dependency", map[string]any{"projectId": p, "edgeId": edge})
		sub := str(obj(c.call("add_subtask", map[string]any{"projectId": p, "parentTaskId": id3, "title": "three a"})["task"])["id"])
		if n := len(c.call("list_subtasks", map[string]any{"projectId": p, "parentTaskId": id3})["tasks"].([]any)); n != 1 {
			t.Fatalf("list_subtasks: %d, want 1", n)
		}
		c.call("add_context_item", map[string]any{"projectId": p, "taskId": id1, "kind": "note", "body": "a note"})
		c.call("set_summary", map[string]any{"projectId": p, "taskId": id1, "version": get(id1)["version"], "summary": "so far"})
		c.call("write_memory", map[string]any{"projectId": p, "taskId": id1, "scope": "task", "key": "k", "value": "v"})
		if n := len(c.call("read_memory", map[string]any{"projectId": p, "taskId": id1})["entries"].([]any)); n != 1 {
			t.Fatalf("read_memory: %d entries, want 1", n)
		}
		c.call("recall_memory", map[string]any{"projectId": p, "key": "k"})
		brief := c.call("get_task_context", map[string]any{"projectId": p, "taskId": id1})
		if _, ok := brief["items"]; !ok {
			t.Fatalf("get_task_context has no items: %v", brief)
		}

		c.call("next_task", map[string]any{"projectId": p, "boardId": b})
		claim := c.call("claim_task", map[string]any{"projectId": p, "taskId": id1})
		lease := str(claim["leaseId"])
		c.call("heartbeat_task", map[string]any{"projectId": p, "taskId": id1, "leaseId": lease})
		c.call("release_task", map[string]any{"projectId": p, "taskId": id1, "leaseId": lease})
		next := c.call("claim_next", map[string]any{"projectId": p, "boardId": b})
		nextID := str(obj(next["task"])["id"])
		c.call("complete_task", map[string]any{"projectId": p, "taskId": nextID, "leaseId": str(next["leaseId"])})
		lease = str(c.call("claim_task", map[string]any{"projectId": p, "taskId": id2})["leaseId"])
		c.call("block_task", map[string]any{"projectId": p, "taskId": id2, "leaseId": lease, "reason": "waiting"})
		lease = str(c.call("claim_task", map[string]any{"projectId": p, "taskId": id3})["leaseId"])
		c.call("fail_task", map[string]any{"projectId": p, "taskId": id3, "leaseId": lease, "reason": "flaky"})
		id4 := str(task("four")["id"])
		lease = str(c.call("claim_task", map[string]any{"projectId": p, "taskId": id4})["leaseId"])
		c.call("handoff_task", map[string]any{"projectId": p, "taskId": id4, "leaseId": lease, "toAgent": "u-grace", "memoryNote": "yours"})
		c.call("reap_stale_leases", map[string]any{"projectId": p, "boardId": b})
		c.call("move_task", map[string]any{"projectId": p, "taskId": id2, "version": get(id2)["version"], "toStatus": "TODO"})
		c.call("cancel_task", map[string]any{"projectId": p, "taskId": id2, "version": get(id2)["version"], "reason": "not needed"})

		att := c.call("attach_file", map[string]any{"projectId": p, "taskId": sub, "fileName": "a.txt", "mimeType": "text/plain", "kind": "file", "bytes": 5})
		if str(att["attachmentId"]) == "" {
			t.Fatalf("attach_file: %v", att)
		}
		if n := len(c.call("list_attachments", map[string]any{"projectId": p, "taskId": sub})["attachments"].([]any)); n != 1 {
			t.Fatalf("list_attachments: %d, want 1", n)
		}
		if n := len(c.call("search_tasks", map[string]any{"projectId": p, "query": "stack"})["results"].([]any)); n == 0 {
			t.Fatal("search_tasks found none of the three tasks")
		}

		pr := map[string]any{"kind": "pr", "url": "https://example.com/pr/1"}
		if got := c.call("add_artifact", map[string]any{"projectId": p, "taskId": id1, "artifacts": []any{pr, map[string]any{"kind": "branch", "ref": "feat/x"}}}); got["added"] != float64(2) {
			t.Fatalf("add_artifact: %v", got)
		}
		if got := c.call("retract_artifact", map[string]any{"projectId": p, "taskId": id1, "artifact": map[string]any{"kind": "branch", "ref": "feat/x"}, "reason": "wrong branch"}); got["retracted"] != float64(1) {
			t.Fatalf("retract_artifact: %v", got)
		}
		c.call("add_comment", map[string]any{"projectId": p, "taskId": id1, "body": "a comment"})
		if n := len(c.call("list_comments", map[string]any{"projectId": p, "taskId": id1})["comments"].([]any)); n != 1 {
			t.Fatalf("list_comments: %d, want 1", n)
		}
		fu := str(obj(c.call("add_follow_up", map[string]any{"projectId": p, "taskId": id1, "body": "a directive"})["followUp"])["id"])
		c.call("ack_follow_up", map[string]any{"projectId": p, "taskId": id1, "followUpId": fu})
		if n := len(c.call("list_follow_ups", map[string]any{"projectId": p, "taskId": id1, "openOnly": true})["followUps"].([]any)); n != 0 {
			t.Fatalf("list_follow_ups openOnly after the ack: %d, want 0", n)
		}

		block := func(id, text string) map[string]any { return map[string]any{"id": id, "text": text} }
		made := c.call("create_chronicle_page", map[string]any{"projectId": p, "kind": "adr", "slug": "adr/0001-stack", "title": "Stack", "blocks": []any{block("a-1", "by hand")}})
		page := str(made["pageId"])
		edited := c.call("edit_chronicle_page", map[string]any{"projectId": p, "pageId": page, "expectedParentVersionId": str(made["versionId"]), "blocks": []any{block("a-1", "edited")}})
		if ing := c.call("ingest_chronicle_page", map[string]any{"projectId": p, "kind": "adr", "slug": "adr/0002-swept", "title": "Swept", "blocks": []any{block("dec1/1", "derived")}}); ing["created"] != true {
			t.Fatalf("ingest_chronicle_page: %v", ing)
		}
		// A real cursor, as the store mints one (base64url of {"order","id"} of the page's last row).
		after := func(order, id string) string {
			raw, _ := json.Marshal(map[string]string{"order": order, "id": id})
			return base64.RawURLEncoding.EncodeToString(raw)
		}

		for _, uri := range []string{
			bk.Scheme + "://" + p + "/board/" + b,
			bk.Scheme + "://" + p + "/task/" + id1,
			bk.Scheme + "://" + p + "/task/" + id1 + "/context",
			bk.Scheme + "://" + p + "/board/" + b + "/feed",
			bk.Scheme + "://" + p + "/chronicle",
			bk.Scheme + "://" + p + "/chronicle?cursor=" + after("adr/0001-stack", page),
			bk.Scheme + "://" + p + "/chronicle/" + page,
			bk.Scheme + "://" + p + "/chronicle/" + page + "/versions",
			bk.Scheme + "://" + p + "/chronicle/" + page + "/versions?cursor=" + after("9999999999999", "CV-zzzzzzzzzzzz"),
			bk.Scheme + "://" + p + "/chronicle/" + page + "/versions/" + str(edited["versionId"]),
		} {
			res, err := c.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: uri})
			if err != nil || len(res.Contents) != 1 || !json.Valid([]byte(res.Contents[0].Text)) {
				t.Fatalf("read %s: %+v, %v", uri, res, err)
			}
		}

		c.call("delete_chronicle_page", map[string]any{"projectId": p, "pageId": page, "expectedHeadVersionId": str(edited["versionId"])})

		oncallOnTheStack(t, h, s, c, p, b)
		runsAndSprintsOnTheStack(t, c, p, b, task, get)
		r23OnTheStack(t, h, c, p, b, task, get)
		grantsOnTheStack(t, c, p)

		c.call("delete_task", map[string]any{"projectId": p, "taskId": id1, "version": get(id1)["version"]})
		sessionsMeAuditOnTheStack(t, h, c, id1)
		c.call("remove_member", map[string]any{"projectId": p, "identityId": "u-grace"})
		c.call("delete_project", map[string]any{"projectId": p})

		var names, missed []string
		for _, e := range h.Server().Manifest() {
			names = append(names, e.Name)
			if c.called[e.Name] == 0 {
				missed = append(missed, e.Name)
			}
		}
		if len(missed) > 0 {
			t.Fatalf("%d registered tools have no real-stack call here: %v", len(missed), missed)
		}
		t.Logf("%s: %d registered tools each answered on the real stack, and 10 resource templates read: %s", s.Engine, len(names), strings.Join(names, " "))
	})
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return slices.Equal(x, y)
}

// oncallOnTheStack drives the nine on-call tools as a workspace admin: the secret answered once and
// never listed, a rotation, a test event filing a ticket, its delivery listed, a kept raw body read,
// the delivery replayed, and the tombstone; a non-admin calling one by name is FORBIDDEN.
func oncallOnTheStack(t *testing.T, h *bk.Harness, s *servicetest.Stack, c *caller, p, b string) {
	t.Helper()
	root := &caller{t: t, cs: h.Connect(t, stackRoot), called: c.called}
	created := root.call("create_integration", map[string]any{"projectId": p, "name": "Stack alerts", "source": "generic", "boardId": b,
		"auth": map[string]any{"mode": "hmac"}, "routing": map[string]any{"mode": "broadcast"}})
	integration := obj(created["integration"])
	iid, secret := str(integration["id"]), str(created["secret"])
	if !strings.HasPrefix(secret, "whsec_") || created["shownOnce"] != true || str(integration["endpointPath"]) != "/hooks/"+iid {
		t.Fatalf("create_integration answered %v", created)
	}
	listed := root.call("list_integrations", map[string]any{"projectId": p})
	if b, _ := json.Marshal(listed); strings.Contains(string(b), secret) || strings.Contains(string(b), `"secret"`) || len(listed["integrations"].([]any)) != 1 {
		t.Fatalf("list_integrations carries a secret, or not the one integration: %s", b)
	}
	updated := obj(root.call("update_integration", map[string]any{"projectId": p, "integrationId": iid, "version": integration["version"],
		"patch": map[string]any{"name": "Stack alerts two"}})["integration"])
	rotated := root.call("rotate_integration_secret", map[string]any{"projectId": p, "integrationId": iid, "version": updated["version"]})
	if str(rotated["secret"]) == secret || str(obj(obj(rotated["integration"])["auth"])["dualAcceptUntil"]) == "" {
		t.Fatalf("rotate_integration_secret answered %v", rotated)
	}
	sent := root.call("send_test_event", map[string]any{"projectId": p, "integrationId": iid})
	if sent["result"] != "created" || str(sent["taskId"]) == "" || sent["doorbell"] != "broadcast" {
		t.Fatalf("send_test_event answered %v", sent)
	}
	dlv := str(sent["deliveryId"])
	if n := len(root.call("list_deliveries", map[string]any{"projectId": p, "integrationId": iid})["deliveries"].([]any)); n != 1 {
		t.Fatalf("list_deliveries: %d rows, want the test event's", n)
	}
	// A raw body is kept by the ingest route for a refused delivery (R2.9); here it is put directly.
	if err := s.Store.PutDeliveryRawBody(testCtx(t), p, iid, dlv, []byte(`{"raw":true}`), 1<<62); err != nil {
		t.Fatal(err)
	}
	if raw := root.call("read_delivery_raw", map[string]any{"projectId": p, "integrationId": iid, "deliveryId": dlv}); str(raw["payloadBase64"]) != "eyJyYXciOnRydWV9" {
		t.Fatalf("read_delivery_raw answered %v", raw)
	}
	if replay := root.call("replay_delivery", map[string]any{"projectId": p, "integrationId": iid, "deliveryId": dlv}); replay["result"] != "created" || replay["replayed"] != false {
		t.Fatalf("replay_delivery answered %v", replay)
	}
	root.call("delete_integration", map[string]any{"projectId": p, "integrationId": iid, "version": obj(rotated["integration"])["version"]})
	if e := bk.ToolError(t, c.cs, "create_integration", map[string]any{"projectId": p, "name": "x", "source": "generic", "boardId": b,
		"auth": map[string]any{"mode": "hmac"}, "routing": map[string]any{"mode": "broadcast"}}); e["code"] != "FORBIDDEN" {
		t.Fatalf("a non-admin's create_integration by name = %v, want FORBIDDEN", e)
	}
}

// sessionsMeAuditOnTheStack drives the agent-session, settings and audit tools (R2.5): the caller's
// own sessions listed, its settings set and read back, grace's session ended by the workspace admin,
// and the audit log naming delete_task's row and the disconnect.
func sessionsMeAuditOnTheStack(t *testing.T, h *bk.Harness, c *caller, deleted string) {
	t.Helper()
	root := &caller{t: t, cs: h.Connect(t, stackRoot), called: c.called}
	if n := len(c.call("list_agent_sessions", map[string]any{})["sessions"].([]any)); n != 1 {
		t.Fatalf("list_agent_sessions: %d sessions, want ada's agent-ada", n)
	}
	if got := c.call("set_my_settings", map[string]any{"maxTicketTokens": 4000, "tokenPolicy": "enforce"}); got["maxTicketTokens"] != float64(4000) {
		t.Fatalf("set_my_settings answered %v", got)
	}
	if got := c.call("get_my_settings", map[string]any{}); got["tokenPolicy"] != "enforce" || got["estimated"] != true {
		t.Fatalf("get_my_settings answered %v", got)
	}
	grace := h.Connect(t, stackGrace)
	if _, err := grace.ListTools(testCtx(t), nil); err != nil {
		t.Fatal(err)
	}
	if got := root.call("disconnect_agent", map[string]any{"identityId": "u-grace", "agentId": "harness"}); got["disconnected"] != true {
		t.Fatalf("disconnect_agent answered %v", got)
	}
	if _, err := grace.ListTools(testCtx(t), nil); err == nil {
		t.Fatal("grace's session answered after disconnect_agent ended it")
	}
	rows := root.call("list_audit_log", map[string]any{"days": 1})["entries"].([]any)
	var actions []string
	for _, r := range rows {
		actions = append(actions, str(obj(r)["action"])+":"+str(obj(r)["subject"]))
	}
	if !slices.Contains(actions, "task.deleted:"+deleted) || !slices.Contains(actions, "agent.disconnected:u-grace") {
		t.Fatalf("list_audit_log: %v, want task.deleted:%s and agent.disconnected:u-grace", actions, deleted)
	}
}

// TestMoveTaskOrderAlone (P-437): move_task with an order and no toColumnId or toStatus reorders the
// task within its own column, read back through get_task, under move_task's own checks: the master
// tier (a worker is FORBIDDEN), another holder's live lease (CONFLICT_LOCKED) and a statuses grant,
// which cannot know the status an order-only move lands in and so refuses it (FORBIDDEN), as it refuses
// a toColumnId-only move. A move naming none of the three is VALIDATION.
func TestMoveTaskOrderAlone(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		grace := &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}}
		p := str(ada.call("create_project", map[string]any{"name": "Reorder"})["projectId"])
		b := str(obj(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		ada.call("add_member", map[string]any{"projectId": p, "identityId": "u-grace", "role": "worker"})
		get := func(id string) map[string]any {
			return obj(ada.call("get_task", map[string]any{"projectId": p, "taskId": id})["task"])
		}
		mk := func(title string) map[string]any {
			return obj(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": title})["task"])
		}
		// first is the task at the column's head, second the one after it; second moves above first.
		first, second := mk("one"), mk("two")
		if str(first["order"]) > str(second["order"]) {
			first, second = second, first
		}
		key, err := domain.OrderBetween("", str(first["order"]))
		if err != nil {
			t.Fatal(err)
		}

		wantCode(t, grace.cs, "move_task", map[string]any{"projectId": p, "taskId": second["id"], "version": second["version"], "order": key}, "FORBIDDEN")
		wantCode(t, ada.cs, "move_task", map[string]any{"projectId": p, "taskId": second["id"], "version": second["version"]}, "VALIDATION")

		moved := obj(ada.call("move_task", map[string]any{"projectId": p, "taskId": second["id"], "version": second["version"], "order": key})["task"])
		got := get(str(second["id"]))
		if moved["order"] != key || got["order"] != key || got["status"] != "TODO" || got["columnId"] != second["columnId"] || str(got["order"]) >= str(first["order"]) {
			t.Fatalf("order alone answered %v and reads back %v; want order %s before %v in column %v, still TODO", moved, got, key, first["order"], second["columnId"])
		}
		page := ada.call("list_tasks", map[string]any{"projectId": p, "boardId": b})["tasks"].([]any)
		if len(page) != 2 || obj(page[0])["id"] != second["id"] {
			t.Fatalf("list_tasks after the reorder: %v, want second first", page)
		}

		// Another holder's live lease refuses it, as it refuses any move.
		grace.call("claim_task", map[string]any{"projectId": p, "taskId": first["id"]})
		held := get(str(first["id"]))
		later, err := domain.OrderBetween(str(held["order"]), "")
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, ada.cs, "move_task", map[string]any{"projectId": p, "taskId": first["id"], "version": held["version"], "order": later}, "CONFLICT_LOCKED")
		if after := get(str(first["id"])); after["order"] != held["order"] || after["status"] != "IN_PROGRESS" {
			t.Fatalf("a refused reorder changed the task: %v, was %v", after, held)
		}

		// A master narrowed to TODO by a statuses grant: the order-only form is refused (the grant
		// cannot see where it lands), the toStatus form naming TODO still reorders.
		ada.call("add_member", map[string]any{"projectId": p, "identityId": "u-grace", "role": "master"})
		ada.call("grant_capabilities", map[string]any{"projectId": p, "identityId": "u-grace", "scope": map[string]any{"statuses": []any{"TODO"}}})
		cur := get(str(second["id"]))
		top, err := domain.OrderBetween("", str(cur["order"]))
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, grace.cs, "move_task", map[string]any{"projectId": p, "taskId": second["id"], "version": cur["version"], "order": top}, "FORBIDDEN")
		if after := get(str(second["id"])); after["order"] != cur["order"] || after["version"] != cur["version"] {
			t.Fatalf("a grant-refused reorder changed the task: %v, was %v", after, cur)
		}
		ok := obj(grace.call("move_task", map[string]any{"projectId": p, "taskId": second["id"], "version": cur["version"], "toStatus": "TODO", "order": top})["task"])
		if ok["order"] != top || get(str(second["id"]))["order"] != top {
			t.Fatalf("the toStatus TODO reorder under the grant answered %v", ok)
		}
	})
}
