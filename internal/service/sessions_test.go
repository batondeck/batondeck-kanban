package service

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

func TestSessionsTouchUpsertsThenCounts(t *testing.T) {
	f := newFixture(t, Options{})
	key := SessionKey("alice", AgentID(f.alice))
	s := f.c.Sessions()
	if _, err := s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: key, AgentName: "Claude", Kind: "oauth_user", ClientName: "claude-code", ClientVersion: "2.1.308"}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"claim_next", "claim_next", "complete_task"} {
		if _, err := s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: key, AgentName: "Claude", Kind: "oauth_user", Tool: tool, ProjectID: f.project.ID}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListAgentSessions(f.ctx, f.alice, ListAgentSessionsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions %+v", got.Sessions)
	}
	a := got.Sessions[0]
	if a.SessionKey != key || a.AgentID != "claude" || a.SessionCount != 1 || a.RequestCount != 3 || a.ToolCounts["claim_next"] != 2 || a.LastTool != "complete_task" || a.LastProjectID != f.project.ID || a.ClientName != "claude-code" || a.Kind != "oauth_user" {
		t.Fatalf("session %+v", a)
	}
	if _, err := time.Parse(time.RFC3339, a.LastSeenAt); err != nil {
		t.Fatalf("lastSeenAt %q: %v", a.LastSeenAt, err)
	}
	if _, err := s.RemoveAgentSession(f.ctx, f.alice, RemoveAgentSessionReq{SessionKey: key}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListAgentSessions(f.ctx, f.alice, ListAgentSessionsReq{})
	if got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("after remove: %#v, want an empty non-nil list", got.Sessions)
	}
	_, err = s.RemoveAgentSession(f.ctx, f.alice, RemoveAgentSessionReq{SessionKey: key})
	wantCode(t, err, domain.CodeNotFound)
}

func TestSessionsRefusals(t *testing.T) {
	f := newFixture(t, Options{})
	s := f.c.Sessions()
	_, err := s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: SessionKey("bob", "x"), AgentName: "n", Kind: "dev"})
	wantCode(t, err, domain.CodeForbidden)
	_, err = s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: "no-separator", AgentName: "n", Kind: "dev"})
	wantCode(t, err, domain.CodeValidation)
	_, err = s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: SessionKey("alice", "x"), AgentName: "n", Kind: "robot"})
	wantCode(t, err, domain.CodeValidation)
	_, err = s.TouchAgentSession(f.ctx, f.alice, TouchAgentSessionReq{SessionKey: SessionKey("alice", "x"), Kind: "dev"})
	wantCode(t, err, domain.CodeValidation)
	_, err = s.RemoveAgentSession(f.ctx, f.alice, RemoveAgentSessionReq{SessionKey: SessionKey("bob", "x")})
	wantCode(t, err, domain.CodeForbidden)
	_, err = s.ListAgentSessions(f.ctx, f.alice, ListAgentSessionsReq{All: true})
	wantCode(t, err, domain.CodeForbidden)
}

// A workspace admin's all lists every identity's sessions; the caller's own list is only its own.
func TestSessionsListAllIsAdminOnly(t *testing.T) {
	f := newFixture(t, Options{})
	s := f.c.Sessions()
	bob := domain.Caller{Identity: "bob"}
	for _, c := range []domain.Caller{f.alice, bob} {
		if _, err := s.TouchAgentSession(f.ctx, c, TouchAgentSessionReq{SessionKey: SessionKey(c.Identity, AgentID(c)), AgentName: "n", Kind: "dev"}); err != nil {
			t.Fatal(err)
		}
	}
	admin := f.alice
	admin.WorkspaceAdmin = true
	all, err := s.ListAgentSessions(f.ctx, admin, ListAgentSessionsReq{All: true})
	if err != nil || len(all.Sessions) != 2 {
		t.Fatalf("admin all: %v %+v", err, all.Sessions)
	}
	own, err := s.ListAgentSessions(f.ctx, bob, ListAgentSessionsReq{})
	if err != nil || len(own.Sessions) != 1 || own.Sessions[0].IdentityID != "bob" || own.Sessions[0].AgentID != "id:bob" {
		t.Fatalf("bob's own: %v %+v", err, own.Sessions)
	}
}

func TestSessionsRegisterWakeSession(t *testing.T) {
	f := newFixture(t, Options{WakeSessionTTL: time.Hour})
	got, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketPath: "/tmp/inbox.sock", Cursor: 7})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.st.GetWakeSession(f.ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w.IdentityID != "alice" || w.AgentID != "claude" || w.SocketPath != "/tmp/inbox.sock" || w.LastCursor != 7 || w.ExpiresAt-w.CreatedAt != time.Hour.Milliseconds() || got.ExpiresAt != isoMs(w.ExpiresAt) {
		t.Fatalf("wake session %+v, resp %+v", w, got)
	}
	_, err = f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{Cursor: -1})
	wantCode(t, err, domain.CodeValidation)
}

func testKeyring(t *testing.T) *core.Keyring {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	k, err := core.OpenKeyring(filepath.Join(t.TempDir(), "unused.key"), func(n string) (string, bool) { return key, n == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A socket token is stored sealed: the row holds no byte run of the plaintext, and the keyring
// opens it only under the row's own AAD.
func TestSessionsRegisterWakeSessionSealsTheToken(t *testing.T) {
	k := testKeyring(t)
	f := newFixture(t, Options{Sealer: k})
	const secret = "inbox-token-0123456789"
	got, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketPath: "/tmp/inbox.sock", SocketToken: secret})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.st.GetWakeSession(f.ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.SocketTokenSealed) == 0 || bytes.Contains(w.SocketTokenSealed, []byte(secret)) {
		t.Fatalf("socket_token_sealed = %q: want a sealed value that does not hold the token", w.SocketTokenSealed)
	}
	pt, err := k.Decrypt(w.SocketTokenSealed, SocketTokenAAD(w.ID))
	if err != nil || string(pt) != secret {
		t.Fatalf("opening the sealed token: %q, %v", pt, err)
	}
	if _, err := k.Decrypt(w.SocketTokenSealed, SocketTokenAAD("W-other")); err == nil {
		t.Fatal("the sealed token opened under another session's AAD")
	}
	// No token: nothing sealed (the positive control for the nil column).
	got, err = f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketPath: "/tmp/inbox.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if w, _ = f.st.GetWakeSession(f.ctx, got.ID); w.SocketTokenSealed != nil {
		t.Fatalf("no token, socket_token_sealed = %q, want nil", w.SocketTokenSealed)
	}
	_, err = f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketToken: secret})
	wantCode(t, err, domain.CodeValidation)
}

// With no sealer a token is refused INTERNAL, naming the setting, and nothing is written.
func TestSessionsRegisterWakeSessionWithoutASealerRefusesAToken(t *testing.T) {
	f := newFixture(t, Options{})
	_, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketPath: "/tmp/inbox.sock", SocketToken: "t"})
	e := wantCode(t, err, domain.CodeInternal)
	if !strings.Contains(e.Message, core.MasterKeyEnv) {
		t.Fatalf("message %q does not name %s", e.Message, core.MasterKeyEnv)
	}
	if _, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, RegisterWakeSessionReq{SocketPath: "/tmp/inbox.sock"}); err != nil {
		t.Fatalf("no token and no sealer: %v, want the session registered", err)
	}
}

// WakeScopes is the caller's own scope plus the broadcast scope of each project it is a member of.
func TestWakeScopes(t *testing.T) {
	f := newFixture(t, Options{})
	got, err := f.c.WakeScopes(f.ctx, f.alice)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alice", BroadcastScope(f.project.ID)}; !slices.Equal(got, want) {
		t.Fatalf("alice's scopes %v, want %v", got, want)
	}
	got, err = f.c.WakeScopes(f.ctx, domain.Caller{Identity: "nobody", TokenScope: "nobody"})
	if err != nil || !slices.Equal(got, []string{"nobody"}) {
		t.Fatalf("a non-member's scopes %v, %v; want only its own", got, err)
	}
	_, err = f.c.WakeScopes(f.ctx, domain.Caller{Identity: "x"})
	wantCode(t, err, domain.CodeInternal)
}
