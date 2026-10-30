package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// REVIEW-L5 B1 (the reviewer's probe, adopted), on every engine: carol mints her own variant of a
// label bob minted first, a case variant ("OPS-BOT" of "ops-bot") and an email form whose local
// part is bob's label ("deploy-bot@evil.example" of "deploy-bot"); a mint compares labels exactly,
// so both pass. A body token that names both labels is the first minter's: ada's "@ops-bot" and
// "@deploy-bot" mention bob alone and ring and notify carol nothing, though bob mints a second
// "ops-bot" token after carol's variant (a label's own date is its first row's, not its latest).
// The other way round, carol mints "ci-bot@evil.example" before bob mints "ci-bot", so "@ci-bot"
// is hers and mentions carol alone. Control: the whole email "@deploy-bot@evil.example" names only
// carol's label, so it still mentions, rings and notifies her.
func TestAMintedVariantOfAnotherLabelDoesNotTakeItsMention(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		ctx := context.Background()
		ada, bob, carol := j.user("ada"), j.user("bob"), j.user("carol")
		clock := time.Now()
		ts := &auth.TokenService{Store: j.st, Now: func() time.Time { clock = clock.Add(time.Second); return clock }}
		for _, m := range []struct{ id, label string }{
			{bob.id, "ops-bot"}, {carol.id, "OPS-BOT"},
			{bob.id, "deploy-bot"}, {carol.id, "deploy-bot@evil.example"},
			{bob.id, "ops-bot"}, // bob's second token of his own label, after carol's variant
			{carol.id, "ci-bot@evil.example"}, {bob.id, "ci-bot"},
		} {
			if _, _, err := ts.Create(ctx, m.id, "", m.label, time.Hour); err != nil {
				t.Fatalf("%s mints %q: %v", m.id, m.label, err)
			}
		}
		j.a.core.SetDeviceSessions(fixedDevices{
			{PID: 701, Role: service.RoleWorker, IdentityID: bob.id, AgentID: "ops-bot"},
			{PID: 702, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "OPS-BOT"},
			{PID: 703, Role: service.RoleWorker, IdentityID: bob.id, AgentID: "deploy-bot"},
			{PID: 704, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "deploy-bot@evil.example"},
			{PID: 705, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "ci-bot@evil.example"},
			{PID: 706, Role: service.RoleWorker, IdentityID: bob.id, AgentID: "ci-bot"},
		})
		p := asStr(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		b := asStr(asMap(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		for _, m := range []*agent{bob, carol} {
			ada.call("add_member", map[string]any{"projectId": p, "identityId": m.id, "role": "worker"})
		}
		task := asStr(asMap(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
		for _, c := range []struct {
			body      string
			want      []string
			carolRung bool
		}{
			{"@ops-bot please look", []string{bob.id}, false},
			{"@deploy-bot please look", []string{bob.id}, false},
			{"@ci-bot please look", []string{carol.id}, true},
			{"@deploy-bot@evil.example please look", []string{carol.id}, true},
		} {
			m, rung, notified := commentOutcome(t, j, ada, p, task, c.body, carol.id)
			t.Logf("%s: %q mentions %v (bob %s, carol %s), carol rung %v, notified %v", j.engine, c.body, m, bob.id, carol.id, rung, notified)
			if !slices.Equal(m, c.want) || rung != c.carolRung || notified != c.carolRung {
				t.Errorf("%s: %q mentions %v, rang carol %v, notified her %v; want %v and %v (bob %s, carol %s)",
					j.engine, c.body, m, rung, notified, c.want, c.carolRung, bob.id, carol.id)
			}
		}
	})
}
