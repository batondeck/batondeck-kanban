package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/retention"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-257 (audit B1): an API token's label is the agent its caller works as, and only that caller
// holds the label (Labelled). The default label names no agent, so the client's own name can.
func TestTheTokenLabelIsTheCallersAgent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.addUser(t, "alice", "pw")
	for _, c := range []struct {
		label, agent string
		labelled     bool
	}{
		{"agent-a", "agent-a", true},
		{"  agent-b\t", "agent-b", true},
		{domain.DefaultTokenLabel, "", false},
		{"bad\x00label", "badlabel", true},
		// 70 characters in 100 bytes, the most a label holds: the agent keeps the first 64.
		{strings.Repeat("é", 30) + strings.Repeat("x", 40), strings.Repeat("é", 30) + strings.Repeat("x", 34), true},
	} {
		tok, _, err := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, c.label, 0)
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.svc.ResolveCaller(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		if got.Agent != c.agent || got.Labelled != c.labelled || got.Via != domain.ViaAPIToken || got.Identity != alice.ID {
			t.Fatalf("label %q: caller %+v, want agent %q labelled %v via api_token", c.label, got, c.agent, c.labelled)
		}
	}
}

func TestAgentLabelTrimsDropsControlsAndCaps(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"  claude-code ":                    "claude-code",
		"a\nb\x7fc":                         "abc",
		strings.Repeat("x", 65):             strings.Repeat("x", 64),
		strings.Repeat("x", 63) + "😀":       strings.Repeat("x", 63), // the emoji is two UTF-16 units: no split
		strings.Repeat("x", 62) + "😀" + "y": strings.Repeat("x", 62) + "😀",
	} {
		if got := domain.AgentLabel(in); got != want {
			t.Fatalf("AgentLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if domain.TokenAgent(" cli ") != "" || domain.TokenAgent("cli2") != "cli2" {
		t.Fatal("TokenAgent: the default label names no agent, any other does")
	}
}

// An OAuth caller's agent is its client's registered client_name, read once per client; it is
// not Labelled, so a self-registered client never holds another agent's label.
func TestAnOAuthCallersAgentIsItsClientName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.svc.OAuth, f.svc.OAuthResource = f.st, "https://bd.test/mcp"
	alice := f.addUser(t, "alice", "pw")
	now := f.clk.now().UnixMilli()
	for _, cl := range []store.OAuthClient{
		{ClientID: "c-1", ClientName: " Claude Desktop\n", RedirectURIs: []string{"http://127.0.0.1/cb"}, Scope: "member", CreatedAt: now},
		{ClientID: "c-2", ClientName: "", RedirectURIs: []string{"http://127.0.0.1/cb"}, Scope: "member", CreatedAt: now},
	} {
		if err := f.st.CreateOAuthClient(ctx, cl); err != nil {
			t.Fatal(err)
		}
	}
	mint := func(client string) string {
		fam := "F-" + client
		if err := f.st.CreateOAuthFamily(ctx, store.OAuthFamily{ID: fam, ClientID: client, IdentityID: alice.ID, Scope: "member", Resource: f.svc.OAuthResource, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		v, h, err := MintOAuthValue(OAuthAccessPrefix)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.CreateOAuthToken(ctx, store.OAuthToken{TokenHash: h, Kind: store.OAuthAccess, FamilyID: fam, ClientID: client, IdentityID: alice.ID, Scope: "member", Resource: f.svc.OAuthResource, CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()}); err != nil {
			t.Fatal(err)
		}
		return v
	}
	named, unnamed := mint("c-1"), mint("c-2")
	got, err := f.svc.ResolveCaller(ctx, named)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "Claude Desktop" || got.Via != domain.ViaOAuth || got.OAuthClientID != "c-1" || got.Labelled {
		t.Fatalf("named client: %+v", got)
	}
	got, err = f.svc.ResolveCaller(ctx, unnamed)
	if err != nil || got.Agent != "" || got.Via != domain.ViaOAuth {
		t.Fatalf("unnamed client: %+v %v", got, err)
	}
}

// P-257 B1 (review): a label is one identity's agent. Minting a label whose agent another
// identity's token carries is VALIDATION; the same identity mints it again, the default label is
// everyone's, and a revoked or expired holder does not free it (review round 2: the first minter
// keeps it).
func TestALabelBelongsToOneIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.addUser(t, "alice", "pw"), f.addUser(t, "bob", "pw")
	mint := func(u store.User, label string) (TokenInfo, error) {
		t.Helper()
		_, info, err := f.svc.Tokens.Create(ctx, u.ID, u.Email, label, 0)
		return info, err
	}
	first, err := mint(alice, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"agent-a", " agent-a\t", "agent-a\x00"} {
		_, err := mint(bob, label)
		if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation || e.Details["label"] != "agent-a" || e.Details["field"] != "label" {
			t.Fatalf("bob mints %q while alice's agent-a is live: %v, want VALIDATION on label agent-a", label, err)
		}
	}
	second, err := mint(alice, "agent-a")
	if err != nil {
		t.Fatalf("alice mints her own label again: %v", err)
	}
	for _, u := range []store.User{alice, bob} {
		if _, err := mint(u, domain.DefaultTokenLabel); err != nil {
			t.Fatalf("%s mints the default label: %v", u.Email, err)
		}
	}
	for _, id := range []string{first.ID, second.ID} {
		if err := f.st.RevokeToken(ctx, id, f.clk.now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mint(bob, "agent-a"); !isValidation(err) {
		t.Fatalf("bob mints agent-a once alice's tokens are revoked: %v, want VALIDATION", err)
	}
	f.clk.add(MaxTokenTTL + time.Hour) // every token of alice's has expired too
	if _, err := mint(bob, "agent-a"); !isValidation(err) {
		t.Fatalf("bob mints agent-a once alice's tokens expired: %v, want VALIDATION", err)
	}
	if _, err := mint(alice, "agent-a"); err != nil {
		t.Fatalf("alice mints her lapsed label back: %v", err)
	}
}

// P-44: the retention sweep deletes lapsed token rows, and label ownership survives it. Alice's two
// scout tokens lapse far past LapsedGrace; the sweep takes the older row and keeps the newest, so
// bob's mint of scout is still VALIDATION and alice still mints it back.
func TestALapsedLabelStaysItsOwnersAfterTheSweep(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.addUser(t, "alice", "pw"), f.addUser(t, "bob", "pw")
	var ids []string
	for range 2 {
		_, info, err := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, "scout", 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
		f.clk.add(time.Millisecond)
	}
	if err := f.st.RevokeToken(ctx, ids[1], f.clk.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.clk.add(MaxTokenTTL + retention.LapsedGrace + time.Hour) // both lapsed more than the grace ago
	swept := (&retention.Sweeper{Store: f.st, Now: f.clk.now, Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}).Sweep(ctx)
	if swept.Tokens != 1 {
		t.Fatalf("swept %d tokens, want 1 (the older scout row)", swept.Tokens)
	}
	if _, err := f.st.GetToken(ctx, ids[0]); err == nil {
		t.Fatal("the older lapsed scout row outlived the sweep")
	}
	if _, err := f.st.GetToken(ctx, ids[1]); err != nil {
		t.Fatalf("the newest scout row: %v, want it kept", err)
	}
	_, _, err := f.svc.Tokens.Create(ctx, bob.ID, bob.Email, "scout", 0)
	if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation || e.Details["label"] != "scout" {
		t.Fatalf("bob mints alice's swept, lapsed scout: %v, want VALIDATION on label scout", err)
	}
	if _, _, err := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, "scout", 0); err != nil {
		t.Fatalf("alice mints her lapsed label back after the sweep: %v", err)
	}
}

func isValidation(err error) bool {
	e, ok := domain.AsBatonDeckError(err)
	return ok && e.Code == domain.CodeValidation && e.Details["label"] == "agent-a"
}
