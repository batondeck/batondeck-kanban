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
	disconnect := DisconnectAgentReq{IdentityID: f.alice.Identity, AgentID: "claude"}
	if _, err := s.DisconnectAgent(f.ctx, f.alice, disconnect); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListAgentSessions(f.ctx, f.alice, ListAgentSessionsReq{})
	if got.Sessions == nil || len(got.Sessions) != 0 {
		t.Fatalf("after disconnect: %#v, want an empty non-nil list", got.Sessions)
	}
	_, err = s.DisconnectAgent(f.ctx, f.alice, disconnect)
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
	// A NUL (which Postgres cannot store) or a field longer than any name the transport writes is
	// refused, the transport's cleaning being the first line.
	long := strings.Repeat("x", 300)
	for _, bad := range []TouchAgentSessionReq{
		{AgentName: "n\x00"}, {AgentName: long}, {AgentName: "n", ClientName: "c\x00"}, {AgentName: "n", ClientVersion: long},
		{AgentName: "n", Tool: "t\x00"}, {AgentName: "n", Tool: long}, {AgentName: "n", Tool: "t", ProjectID: "P\x00"}, {AgentName: "n", OAuthClientID: long},
	} {
		bad.SessionKey, bad.Kind = SessionKey("alice", "x"), "dev"
		_, err = s.TouchAgentSession(f.ctx, f.alice, bad)
		wantCode(t, err, domain.CodeValidation)
	}
	_, err = s.DisconnectAgent(f.ctx, f.alice, DisconnectAgentReq{IdentityID: "bob", AgentID: "x"})
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

func testKeyring(t *testing.T) *core.Keyring {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	k, err := core.OpenKeyring(filepath.Join(t.TempDir(), "unused.key"), func(n string) (string, bool) { return key, n == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// worker is a valid directory entry for alice; the tests vary one field at a time.
func worker() RegisterWakeSessionReq {
	return RegisterWakeSessionReq{PID: 4242, Role: RoleWorker, SocketPath: "/tmp/cc-socks/4242.sock", ClientSessionID: "f73a2587", ProcStart: "Fri Oct 23 08:36:23 2026", Cursor: 7, Bearer: "bk_bearer-0123456789"}
}

// An entry is stored with every directory field, for the TTL, and lists back with its bearer opened;
// a re-register under its id replaces it; the cursor and the drop are one call each.
func TestSessionsRegisterWakeSession(t *testing.T) {
	f := newFixture(t, Options{WakeSessionTTL: time.Hour, Sealer: testKeyring(t)})
	got, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, worker())
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.st.GetWakeSession(f.ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w.IdentityID != "alice" || w.AgentID != "claude" || got.AgentID != "claude" || w.SocketPath != "/tmp/cc-socks/4242.sock" || w.LastCursor != 7 || w.PID != 4242 || w.Role != RoleWorker ||
		w.ClientSessionID != "f73a2587" || w.ProcStart != "Fri Oct 23 08:36:23 2026" || w.ExpiresAt-w.CreatedAt != time.Hour.Milliseconds() || got.ExpiresAt != isoMs(w.ExpiresAt) {
		t.Fatalf("wake session %+v, resp %+v", w, got)
	}
	rows, err := f.c.WakeStore().ListWakeSessions(f.ctx)
	if err != nil || len(rows) != 1 || rows[0].Bearer != "bk_bearer-0123456789" || !rows[0].Sealed || rows[0].PID != 4242 || rows[0].Role != RoleWorker {
		t.Fatalf("ListWakeSessions = %+v, %v", rows, err)
	}
	again := worker()
	again.ID, again.Role, again.Agent = got.ID, RoleMaster, "planner"
	if _, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, again); err != nil {
		t.Fatal(err)
	}
	if w, _ = f.st.GetWakeSession(f.ctx, got.ID); w.Role != RoleMaster || w.AgentID != "planner" {
		t.Fatalf("after the re-register %+v", w)
	}
	other := worker()
	_, err = f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, other) // a new id for a pid already held
	wantCode(t, err, domain.CodeValidation)
	if err := f.c.WakeStore().SetWakeSessionCursor(f.ctx, got.ID, 99); err != nil {
		t.Fatal(err)
	}
	if w, _ = f.st.GetWakeSession(f.ctx, got.ID); w.LastCursor != 99 {
		t.Fatalf("cursor %d, want 99", w.LastCursor)
	}
	if err := f.c.WakeStore().DropWakeSession(f.ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.c.WakeStore().DropWakeSession(f.ctx, got.ID); err != nil {
		t.Fatalf("dropping a missing row: %v, want nil", err)
	}
	for _, bad := range []func(*RegisterWakeSessionReq){
		func(r *RegisterWakeSessionReq) { r.Cursor = -1 },
		func(r *RegisterWakeSessionReq) { r.PID = 0 },
		func(r *RegisterWakeSessionReq) { r.Role = "admin" },
		func(r *RegisterWakeSessionReq) { r.Bearer = "" },
		func(r *RegisterWakeSessionReq) { r.SocketPath, r.SocketToken = "", "t" },
	} {
		req := worker()
		bad(&req)
		_, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, req)
		wantCode(t, err, domain.CodeValidation)
	}
}

// The bearer and the socket token are stored sealed: the row holds no byte run of either, and the
// keyring opens each only under the row's own AAD.
func TestSessionsRegisterWakeSessionSealsTheSecrets(t *testing.T) {
	k := testKeyring(t)
	f := newFixture(t, Options{Sealer: k})
	const secret = "inbox-token-0123456789"
	req := worker()
	req.SocketToken = secret
	got, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, req)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.st.GetWakeSession(f.ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sealed []byte
		plain  string
		aad    func(string) []byte
	}{{w.SocketTokenSealed, secret, SocketTokenAAD}, {w.BearerSealed, req.Bearer, BearerAAD}} {
		if len(c.sealed) == 0 || bytes.Contains(c.sealed, []byte(c.plain)) {
			t.Fatalf("sealed %q: want a sealed value that does not hold %q", c.sealed, c.plain)
		}
		if pt, err := k.Decrypt(c.sealed, c.aad(w.ID)); err != nil || string(pt) != c.plain {
			t.Fatalf("opening %q: %q, %v", c.plain, pt, err)
		}
		if _, err := k.Decrypt(c.sealed, c.aad("W-other")); err == nil {
			t.Fatal("a secret opened under another session's AAD")
		}
	}
	// A row sealed under another key lists unopened, so the directory drops it.
	w.BearerSealed, _ = k.Encrypt([]byte("x"), BearerAAD("W-other"))
	if err := f.st.PutWakeSession(f.ctx, w); err != nil {
		t.Fatal(err)
	}
	rows, err := f.c.WakeStore().ListWakeSessions(f.ctx)
	if err != nil || len(rows) != 1 || rows[0].Sealed || rows[0].Bearer != "" {
		t.Fatalf("a row whose bearer does not open: %+v, %v", rows, err)
	}
}

// With no sealer an entry is refused INTERNAL, naming the setting, and nothing is written.
func TestSessionsRegisterWakeSessionWithoutASealerIsRefused(t *testing.T) {
	f := newFixture(t, Options{})
	_, err := f.c.Sessions().RegisterWakeSession(f.ctx, f.alice, worker())
	e := wantCode(t, err, domain.CodeInternal)
	if !strings.Contains(e.Message, core.MasterKeyEnv) {
		t.Fatalf("message %q does not name %s", e.Message, core.MasterKeyEnv)
	}
	if rows, _ := f.st.ListWakeSessions(f.ctx); len(rows) != 0 {
		t.Fatalf("rows %+v written without a sealer", rows)
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
