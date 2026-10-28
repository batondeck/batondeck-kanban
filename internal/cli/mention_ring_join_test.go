package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// fixedDevices is a fixed device session directory: the entries serve's directory would list.
type fixedDevices []service.DeviceSession

func (d fixedDevices) Entries() []service.DeviceSession { return d }
func (d fixedDevices) Changed() <-chan struct{}         { return make(chan struct{}) }

// TestCommentMentionRingsTheMentionedWorkerOnTheRealStack (P-310, owner decision 5, the exception
// to ADR-0004's "comments ring nobody"): on every selected engine, over the serve composition and
// real API tokens, add_comment over MCP writes one assigned change row, scoped to the mentioned
// member, for a mention of a member with a live worker session, by login or by the worker's agent
// label, and the mention notification beside it. A comment with no mention, a self-mention, a
// member with only a master session, a member with no session, an unknown name and a non-member
// write no doorbell: the one "changed" row a comment always wrote, and nothing scoped to anyone.
// It lives here, not in internal/mcp, because only the composition root may read the change log
// (ADR-0003 §C row 6). The directory is a fixed one, set on the composed core after compose.
func TestCommentMentionRingsTheMentionedWorkerOnTheRealStack(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		e, st := j.engine, j.st
		ctx := context.Background()
		ada, will, maxim, mona := j.user("ada"), j.user("will"), j.user("max"), j.user("mona")
		j.a.core.SetDeviceSessions(fixedDevices{
			{PID: 101, Role: service.RoleWorker, IdentityID: will.id, AgentID: "claude-will"},
			{PID: 102, Role: service.RoleMaster, IdentityID: mona.id, AgentID: "claude-mona"},
			{PID: 103, Role: service.RoleWorker, IdentityID: ada.id, AgentID: "id:" + ada.id},
		})
		j.user("grace") // an account, never made a member
		p := asStr(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		b := asStr(asMap(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		for _, m := range []struct {
			a    *agent
			role string
		}{{will, "worker"}, {maxim, "worker"}, {mona, "master"}} {
			ada.call("add_member", map[string]any{"projectId": p, "identityId": m.a.id, "role": m.role})
		}
		task := asStr(asMap(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
		since, err := st.ReadChanges(ctx, 0, 100000)
		if err != nil {
			t.Fatal(err)
		}
		last := since[len(since)-1].ID

		// comment adds body as ada and is its doorbell rows: its change rows but the notification ones.
		comment := func(body string) (map[string]any, []store.Change) {
			t.Helper()
			cm := asMap(ada.call("add_comment", map[string]any{"projectId": p, "taskId": task, "body": body})["comment"])
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
				t.Fatalf("%q wrote %s; want one assigned row scoped %s", body, describeRows(rows), want)
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
				found = found || (r.Type == domain.NotificationMention && d.CommentID == asStr(cm["id"]))
			}
			if !found {
				t.Fatalf("%q rang %s but wrote no mention notification for it", body, want)
			}
			t.Logf("%s: %q wrote %s and %s's mention notification", e, body, describeRows(rows), want)
		}
		silent := func(body string, mentions int) {
			t.Helper()
			cm, rows := comment(body)
			if len(rows) != 1 || rows[0].Kind != service.ChangeKindChanged || rows[0].Scope != service.BroadcastScope(p) {
				t.Fatalf("%q wrote %s; want only the one changed row on %s, no doorbell", body, describeRows(rows), service.BroadcastScope(p))
			}
			if ms, _ := cm["mentions"].([]any); len(ms) != mentions {
				t.Fatalf("%q mentioned %v, want %d", body, cm["mentions"], mentions)
			}
			t.Logf("%s: %q wrote %s", e, body, describeRows(rows))
		}

		rang("@will please look", will.id)        // the positive control, by login
		rang("@"+will.id+" please look", will.id) // by identity
		rang("@claude-will over to you", will.id) // by the worker's label
		silent("looks good", 0)                   // no mention: ADR-0004 holds
		silent("@ada note to self", 1)            // a self-mention, though ada has a worker
		silent("@mona fyi", 1)                    // a master session only
		silent("@claude-mona fyi", 0)             // a master's label is no alias
		silent("@max fyi", 1)                     // a member with no session: a human
		silent("@nobody @grace hello", 0)         // an unknown name and a non-member
	})
}

func describeRows(rows []store.Change) string {
	out := "["
	for i, r := range rows {
		if i > 0 {
			out += " "
		}
		out += r.Kind + "@" + r.Scope
	}
	return out + "]"
}
