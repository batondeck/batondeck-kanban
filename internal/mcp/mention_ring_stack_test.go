package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// deviceSessions is a fixed device session directory: the entries serve's directory would list.
type deviceSessions []service.DeviceSession

func (d deviceSessions) Entries() []service.DeviceSession { return d }
func (d deviceSessions) Changed() <-chan struct{}         { return make(chan struct{}) }

// TestCommentMentionRingsTheMentionedWorkerOnTheRealStack (P-310, owner decision 5, the exception
// to ADR-0004's "comments ring nobody"): on every selected engine, add_comment over MCP writes one
// assigned change row, scoped to the mentioned member, for a mention of a member with a live worker
// session, by identity or by the worker's agent label, and the mention notification beside it. A
// comment with no mention, a self-mention (by identity and by label), a member with only a master
// session, a member with no session, an unknown name and a non-member write no doorbell: the one
// "changed" row a comment always wrote, and nothing scoped to anyone.
func TestCommentMentionRingsTheMentionedWorkerOnTheRealStack(t *testing.T) {
	res := authfake.Resolver{Callers: map[string]domain.Caller{
		notesAda:   {Identity: "u-ada", TokenScope: "u-ada"},
		notesWill:  {Identity: "u-will", TokenScope: "u-will"},
		notesGrace: {Identity: "u-grace", TokenScope: "u-grace"},
	}}
	for _, e := range servicetest.Engines(t) {
		t.Run(e, func(t *testing.T) {
			ctx := context.Background()
			st := servicetest.OpenStore(t, e)
			c := service.New(st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(ctx) })
			c.SetDeviceSessions(deviceSessions{
				{PID: 101, Role: service.RoleWorker, IdentityID: "u-will", AgentID: "claude-will"},
				{PID: 102, Role: service.RoleMaster, IdentityID: "u-mona", AgentID: "claude-mona"},
				{PID: 103, Role: service.RoleWorker, IdentityID: "u-ada", AgentID: "id:u-ada"},
			})
			svc := servicetest.ServicesOf(c)
			h := bk.BootHarness(t, svc, res)
			tools.Register(h.Server(), svc)
			ada := &caller{t: t, cs: h.Connect(t, notesAda), called: map[string]int{}}
			p := str(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
			b := str(obj(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
			for id, role := range map[string]string{"u-will": "worker", "u-max": "worker", "u-mona": "master"} {
				ada.call("add_member", map[string]any{"projectId": p, "identityId": id, "role": role})
			}
			task := str(obj(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
			since, err := st.ReadChanges(ctx, 0, 100000)
			if err != nil {
				t.Fatal(err)
			}
			last := since[len(since)-1].ID

			// comment adds body as ada and is its doorbell rows: its change rows but the notification ones.
			comment := func(body string) (map[string]any, []store.Change) {
				t.Helper()
				cm := obj(ada.call("add_comment", map[string]any{"projectId": p, "taskId": task, "body": body})["comment"])
				rows, err := st.ReadChanges(ctx, last, 1000)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) > 0 {
					last = rows[len(rows)-1].ID
				}
				// A notification row (the feed's, R2.3) is no doorbell: the directory rings none.
				bells := rows[:0:0]
				for _, r := range rows {
					if r.Kind != service.ChangeKindNotification {
						bells = append(bells, r)
					}
				}
				return cm, bells
			}
			rang := func(body string, want string) {
				t.Helper()
				cm, rows := comment(body)
				if len(rows) != 1 || rows[0].Kind != service.DoorbellAssigned || rows[0].Scope != want {
					t.Fatalf("%q wrote %s; want one assigned row scoped %s", body, describe(rows), want)
				}
				var pl struct{ Agent, TaskID, Tool string }
				_ = json.Unmarshal(rows[0].Payload, &pl)
				if pl.Agent != want || pl.TaskID != task || pl.Tool != "add_comment" {
					t.Fatalf("%q: the row's payload %s", body, rows[0].Payload)
				}
				n, err := st.ListNotifications(ctx, want, store.NotificationsQuery{Limit: 200})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, r := range n.Items {
					var d struct{ CommentID string }
					_ = json.Unmarshal(r.Data, &d)
					found = found || (r.Type == domain.NotificationMention && d.CommentID == str(cm["id"]))
				}
				if !found {
					t.Fatalf("%q rang %s but wrote no mention notification for it", body, want)
				}
				t.Logf("%s: %q wrote %s and %s's mention notification", e, body, describe(rows), want)
			}
			silent := func(body string, mentions int) {
				t.Helper()
				cm, rows := comment(body)
				if len(rows) != 1 || rows[0].Kind != service.ChangeKindChanged || rows[0].Scope != service.BroadcastScope(p) {
					t.Fatalf("%q wrote %s; want only the one changed row on %s, no doorbell", body, describe(rows), service.BroadcastScope(p))
				}
				if ms, _ := cm["mentions"].([]any); len(ms) != mentions {
					t.Fatalf("%q mentioned %v, want %d", body, cm["mentions"], mentions)
				}
				t.Logf("%s: %q wrote %s", e, body, describe(rows))
			}

			rang("@u-will please look", "u-will")      // the positive control, by identity
			rang("@claude-will over to you", "u-will") // by the worker's label
			silent("looks good", 0)                    // no mention: ADR-0004 holds
			silent("@u-ada note to self", 1)           // a self-mention, though ada has a worker
			silent("@u-mona fyi", 1)                   // a master session only
			silent("@claude-mona fyi", 0)              // a master's label is no alias
			silent("@u-max fyi", 1)                    // a member with no session: a human
			silent("@nobody @u-grace hello", 0)        // an unknown name and a non-member
		})
	}
}

func describe(rows []store.Change) string {
	out := "["
	for i, r := range rows {
		if i > 0 {
			out += " "
		}
		out += r.Kind + "@" + r.Scope
	}
	return out + "]"
}
