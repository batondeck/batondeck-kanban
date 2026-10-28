package service

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-322 (with Fable B1 on #78, P-343): a directory entry takes only an agent its caller may claim
// for. On every engine, RegisterWakeSession refuses FORBIDDEN, writing no row, an agent that is
// another agent's label by ownLabel's own test (another identity's token row of any status carries
// it, or a live token does and the caller is not that token), and the same caller's
// claim_next {assignee: <that agent>} is refused alike; every accepted agent's claim is not refused
// for the label. No master pass at register: carol is a worker here, and alice, the project's admin,
// is refused bob's label too (no admin override, REVIEW-257 R2-Q2).
func TestAWakeEntryTakesOnlyAnAgentItsCallerMayClaimFor(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		_ = e.c.Close(context.Background())
		e.c = New(e.st, Options{Logger: log.New(&logBuf{}, "", 0), Now: e.clock.now, ToolCallFlush: -1, Sealer: testKeyring(t), WakeSessionTTL: time.Hour})
		t.Cleanup(func() { _ = e.c.Close(context.Background()) })
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		hour := time.Hour.Milliseconds()
		for _, tok := range []store.Token{
			{ID: "tok-bob", IdentityID: "bob", Subject: "bob", SecretHash: "h-1", Label: "bob-live", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-bob-dead", IdentityID: "bob", Subject: "bob", SecretHash: "h-2", Label: "bob-dead", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-bob-old", IdentityID: "bob", Subject: "bob", SecretHash: "h-3", Label: "bob-old", CreatedAt: now - 2*hour, ExpiresAt: now - hour},
			{ID: "tok-carol-a", IdentityID: "carol", Subject: "carol", SecretHash: "h-4", Label: "carol-a", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-carol-b", IdentityID: "carol", Subject: "carol", SecretHash: "h-5", Label: "carol-b", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-carol-old", IdentityID: "carol", Subject: "carol", SecretHash: "h-6", Label: "carol-old", CreatedAt: now - 2*hour, ExpiresAt: now - hour},
		} {
			if err := e.st.CreateToken(e.ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.st.RevokeToken(e.ctx, "tok-bob-dead", now); err != nil {
			t.Fatal(err)
		}
		carolA := carol // on her carol-a token
		carolA.Agent, carolA.Labelled, carolA.Via = "carol-a", true, domain.ViaAPIToken
		carolCLI := carol // on a default-label token: no agent
		carolCLI.Via = domain.ViaAPIToken
		carolOAuth := carol // an OAuth client that named itself after bob's label
		carolOAuth.Agent, carolOAuth.Via = "bob-live", domain.ViaOAuth
		_ = bob

		cases := []struct {
			name    string
			who     domain.Caller
			agent   string // as registered; "" for the caller's own AgentID
			refused bool
		}{
			{"bob's live label", carolA, "bob-live", true},
			{"bob's revoked label", carolA, "bob-dead", true},
			{"bob's expired label", carolA, "bob-old", true},
			{"bob's label, the project's admin", e.alice, "bob-live", true},
			{"her own other live label, from carol-a's token", carolA, "carol-b", true},
			{"her own live label, from a default token", carolCLI, "carol-a", true},
			{"no agent, an OAuth client named bob-live", carolOAuth, "", true},
			{"her own label on its token", carolA, "carol-a", false},
			{"no agent, on her labelled token", carolA, "", false},
			{"her own lapsed label", carolCLI, "carol-old", false},
			{"a name no token carries", carolA, "claude-rev", false},
			{"no agent, on a default token", carolCLI, "", false},
		}
		accepted := 0
		for i, c := range cases {
			req := worker()
			req.PID, req.Agent = int64(5000+i), c.agent
			agent := c.agent
			if agent == "" {
				agent = AgentID(c.who)
			}
			got, err := e.c.Sessions().RegisterWakeSession(e.ctx, c.who, req)
			if c.refused {
				be := wantCode(t, err, domain.CodeForbidden)
				if be.Details["field"] != "agent" || be.Details["label"] != agent {
					t.Fatalf("%s: register details %v, want field agent, label %q", c.name, be.Details, agent)
				}
			} else {
				if err != nil {
					t.Fatalf("%s: register under %q: %v", c.name, agent, err)
				}
				if got.AgentID != agent {
					t.Fatalf("%s: registered as %q, want %q", c.name, got.AgentID, agent)
				}
				accepted++
			}
			rows, err := e.c.WakeStore().ListWakeSessions(e.ctx)
			if err != nil || len(rows) != accepted {
				t.Fatalf("%s: %d rows (%v), want %d: a refused register writes none", c.name, len(rows), err, accepted)
			}
			if agent == AgentID(domain.Caller{Identity: c.who.Identity}) {
				continue // the caller's derived id names no label to claim for
			}
			// The claim agrees with the register, for the same caller and label.
			_, err = e.c.Lifecycle().ClaimNext(e.ctx, c.who, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: agent})
			forbiddenClaim := false
			if be, ok := domain.AsBatonDeckError(err); ok && be.Code == domain.CodeForbidden {
				forbiddenClaim = true
			}
			if c.refused && c.who.Identity == e.alice.Identity {
				// The admin's claim passes (ownLabel's master pass); the register has none.
				if forbiddenClaim {
					t.Fatalf("%s: the admin's claim_next of %q was refused: %v", c.name, agent, err)
				}
				continue
			}
			if forbiddenClaim != c.refused {
				t.Fatalf("%s: register refused %v but claim_next {assignee: %q} refused %v (%v): the two must agree", c.name, c.refused, agent, forbiddenClaim, err)
			}
		}
	})
}
