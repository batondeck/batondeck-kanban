package service

import (
	"testing"
	"time"

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
