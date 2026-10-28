package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-343 (Fable B1 on #78, its probe adopted): a mention of a token label rings and notifies only the
// identity that owns the label (P-257: its first minter, REVIEW-257 R2-Q1). On every engine, over the
// serve composition and real API tokens, carol's worker entries under bob's label, and under the
// variants a mention reads as that label (case, spaces, an email's local part), alias nothing: ada's
// "@<bob's label>" rings bob alone and notifies nobody else. The entries are set on the composed
// core, as the directory would hold rows written before P-322's register check, so this pins the
// alias rule itself (liveWorkers' ownedLabels), whatever the ingress let in.
func TestAMentionOfALabelRingsAndNotifiesOnlyItsOwner(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		st := j.st
		ctx := context.Background()
		ada, bob, carol := j.user("ada"), j.user("bob"), j.user("carol")
		// bob's real token carries joinLabel("bob"); carol's worker entries claim that label.
		j.a.core.SetDeviceSessions(fixedDevices{
			{PID: 201, Role: service.RoleWorker, IdentityID: bob.id, AgentID: joinLabel("bob")},
			{PID: 202, Role: service.RoleWorker, IdentityID: carol.id, AgentID: joinLabel("bob")},
			{PID: 203, Role: service.RoleWorker, IdentityID: carol.id, AgentID: strings.ToUpper(joinLabel("bob"))},
			{PID: 204, Role: service.RoleWorker, IdentityID: carol.id, AgentID: " " + joinLabel("bob") + " "},
			{PID: 205, Role: service.RoleWorker, IdentityID: carol.id, AgentID: joinLabel("bob") + "@carol.example"},
		})
		p := asStr(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		b := asStr(asMap(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		for _, m := range []*agent{bob, carol} {
			ada.call("add_member", map[string]any{"projectId": p, "identityId": m.id, "role": "worker"})
		}
		task := asStr(asMap(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
		since, err := st.ReadChanges(ctx, 0, 100000)
		if err != nil {
			t.Fatal(err)
		}
		last := since[len(since)-1].ID

		cm := asMap(ada.call("add_comment", map[string]any{"projectId": p, "taskId": task, "body": "@" + joinLabel("bob") + " please look"})["comment"])
		rows, err := st.ReadChanges(ctx, last, 1000)
		if err != nil {
			t.Fatal(err)
		}
		var bells []store.Change
		for _, r := range rows {
			if r.Kind != service.ChangeKindNotification {
				bells = append(bells, r)
			}
		}
		scopes := map[string]bool{}
		for _, r := range bells {
			scopes[r.Kind+"@"+r.Scope] = true
		}
		carolNotified := false
		n, err := st.ListNotifications(ctx, carol.id, store.NotificationsQuery{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range n.Items {
			var d struct{ CommentID string }
			_ = json.Unmarshal(r.Data, &d)
			carolNotified = carolNotified || (r.Type == domain.NotificationMention && d.CommentID == asStr(cm["id"]))
		}
		t.Logf("%s: @%s wrote %s; carol notified: %v; mentions %v", j.engine, joinLabel("bob"), describeRows(bells), carolNotified, cm["mentions"])
		if scopes["assigned@"+carol.id] || carolNotified {
			t.Fatalf("carol, registered under bob's label %q or a variant of it, was rung (%s) or notified (%v) for a mention of bob's label",
				joinLabel("bob"), describeRows(bells), carolNotified)
		}
		if !scopes["assigned@"+bob.id] {
			t.Fatalf("control: bob (the label's owner) was not rung: %s", describeRows(bells))
		}
		if m, _ := cm["mentions"].([]any); len(m) != 1 || !slices.Contains(m, any(bob.id)) {
			t.Fatalf("@%s mentions %v, want bob's identity alone", joinLabel("bob"), cm["mentions"])
		}
		// carol's live worker still rings for her own login: only the alias was dropped.
		before, err := st.ReadChanges(ctx, 0, 100000)
		if err != nil {
			t.Fatal(err)
		}
		ada.call("add_comment", map[string]any{"projectId": p, "taskId": task, "body": "@carol and you"})
		rows, err = st.ReadChanges(ctx, before[len(before)-1].ID, 1000)
		if err != nil {
			t.Fatal(err)
		}
		rung := false
		for _, r := range rows {
			rung = rung || (r.Kind != service.ChangeKindNotification && r.Kind+"@"+r.Scope == "assigned@"+carol.id)
		}
		if !rung {
			t.Fatalf("ada's @carol did not ring carol's live worker: %s", describeRows(rows))
		}
	})
}
