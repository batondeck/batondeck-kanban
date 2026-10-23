package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// sessions is service.Sessions over store.AgentSessions and store.WakeSessions. It writes no
// event, audit row or doorbell.
type sessions struct{ c *Core }

var (
	_ Sessions  = sessions{}
	_ WakeStore = sessions{}
)

// Sessions is the agent and wake session service.
func (c *Core) Sessions() Sessions { return sessions{c} }

// WakeStore is the device session directory's persistence.
func (c *Core) WakeStore() WakeStore { return sessions{c} }

// SessionKey is an agent session's key, conductor's `${identityId}::${agentId}`: one row per agent
// of an identity.
func SessionKey(identityID, agentID string) string { return identityID + "::" + agentID }

// AgentID is the caller's agent id: the transport's, or conductor's derived "id:<identity>" when
// the call names no agent.
func AgentID(caller domain.Caller) string {
	if caller.Agent != "" {
		return caller.Agent
	}
	return "id:" + caller.Identity
}

// ownKey splits a session key and refuses one that is not the caller's identity's.
func ownKey(caller domain.Caller, key string) (string, error) {
	identity, agent, ok := strings.Cut(key, "::")
	if !ok || identity == "" || agent == "" {
		return "", domain.Validation("sessionKey must be <identityId>::<agentId>", map[string]any{"field": "sessionKey"})
	}
	if identity != caller.Identity {
		return "", forbidden("Not your agent session", map[string]any{"sessionKey": key})
	}
	return agent, nil
}

// TouchAgentSession has two forms, told apart by req.Tool (the convention the transport follows;
// contract_sessions.go's "records one request" is the second form). Tool "" is the MCP initialize:
// a connection, so the session count grows and the client fields are written
// (store.UpsertAgentSession). A tool name is one tools/call: the request and per-tool counts grow
// (store.TouchAgentSession). A CLI bearer token's session is SessionKind service_account.
func (s sessions) TouchAgentSession(ctx context.Context, caller domain.Caller, req TouchAgentSessionReq) (resp TouchAgentSessionResp, err error) {
	defer s.c.recovered("touch_agent_session", &err)
	agent, err := ownKey(caller, req.SessionKey)
	if err != nil {
		return resp, err
	}
	if req.AgentName == "" {
		return resp, domain.Validation("agentName is required", map[string]any{"field": "agentName"})
	}
	if !slices.Contains(SessionKindValues, req.Kind) {
		return resp, domain.Validation("unknown session kind: "+string(req.Kind), map[string]any{"field": "kind", "allowed": SessionKindValues})
	}
	at := s.c.nowMs()
	if req.Tool == "" {
		_, err = s.c.store.UpsertAgentSession(ctx, store.AgentSessionStart{
			IdentityID: caller.Identity, AgentID: agent, AgentName: req.AgentName, Kind: string(req.Kind),
			ClientName: req.ClientName, ClientVersion: req.ClientVersion, OAuthClientID: req.OAuthClientID, At: at,
		})
	} else {
		err = s.c.store.TouchAgentSession(ctx, store.AgentSessionTouch{
			IdentityID: caller.Identity, AgentID: agent, AgentName: req.AgentName, Kind: string(req.Kind),
			Tool: req.Tool, ProjectID: req.ProjectID, Count: 1, At: at,
		})
	}
	return resp, s.c.mapErr("touch_agent_session", err)
}

func (s sessions) RemoveAgentSession(ctx context.Context, caller domain.Caller, req RemoveAgentSessionReq) (resp RemoveAgentSessionResp, err error) {
	defer s.c.recovered("remove_agent_session", &err)
	agent, err := ownKey(caller, req.SessionKey)
	if err != nil {
		return resp, err
	}
	return resp, s.c.mapErr("remove_agent_session", s.c.store.DeleteAgentSession(ctx, caller.Identity, agent))
}

// ListAgentSessions is the caller's own sessions, or every identity's for a workspace admin asking
// for all; a non-admin asking for all is FORBIDDEN.
func (s sessions) ListAgentSessions(ctx context.Context, caller domain.Caller, req ListAgentSessionsReq) (resp ListAgentSessionsResp, err error) {
	defer s.c.recovered("list_agent_sessions", &err)
	identity := caller.Identity
	if req.All {
		if !caller.WorkspaceAdmin {
			return resp, forbidden("Workspace admin required to list every identity's sessions", map[string]any{"requiredCapability": "workspace-admin"})
		}
		identity = ""
	}
	rows, err := s.c.store.ListAgentSessions(ctx, identity, int(req.Limit))
	if err != nil {
		return resp, s.c.mapErr("list_agent_sessions", err)
	}
	resp.Sessions = make([]AgentSession, 0, len(rows))
	for _, r := range rows {
		counts := make(map[string]float64, len(r.ToolCounts))
		for k, v := range r.ToolCounts {
			counts[k] = float64(v)
		}
		resp.Sessions = append(resp.Sessions, AgentSession{
			AgentID: r.AgentID, AgentName: r.AgentName, FirstSeenAt: isoMs(r.FirstSeenAt), IdentityID: r.IdentityID,
			Kind: SessionKind(r.Kind), LastSeenAt: isoMs(r.LastSeenAt), RequestCount: float64(r.RequestCount),
			SessionCount: float64(r.SessionCount), SessionKey: SessionKey(r.IdentityID, r.AgentID), ToolCounts: counts,
			ClientName: r.ClientName, ClientVersion: r.ClientVersion, LastProjectID: r.LastProjectID, LastTool: r.LastTool,
		})
	}
	return resp, nil
}

// SocketTokenAAD binds a sealed inbox token to its wake session's row.
func SocketTokenAAD(id string) []byte { return []byte("wake_sessions.socket_token:" + id) }

// BearerAAD binds a sealed directory bearer to its wake session's row.
func BearerAAD(id string) []byte { return []byte("wake_sessions.bearer:" + id) }

// RegisterWakeSession records a directory entry for the wake-session TTL. The bearer and any socket
// token are stored sealed (Options.Sealer), never as given; with no sealer the entry is refused,
// since its bearer could not be re-resolved after a restart.
func (s sessions) RegisterWakeSession(ctx context.Context, caller domain.Caller, req RegisterWakeSessionReq) (resp RegisterWakeSessionResp, err error) {
	defer s.c.recovered("register_wake_session", &err)
	switch {
	case req.Cursor < 0:
		return resp, domain.Validation("cursor cannot be negative", map[string]any{"field": "cursor"})
	case req.PID <= 0:
		return resp, domain.Validation("pid must be a positive process id", map[string]any{"field": "pid"})
	case req.Role != RoleMaster && req.Role != RoleWorker:
		return resp, domain.Validation("role must be master or worker", map[string]any{"field": "role"})
	case req.SocketToken != "" && req.SocketPath == "":
		return resp, domain.Validation("socketToken needs a socketPath", map[string]any{"field": "socketToken"})
	case req.Bearer == "":
		return resp, domain.Validation("a directory entry needs the bearer it registered with", nil)
	case s.c.sealer == nil:
		return resp, domain.NewError(domain.CodeInternal, "No keyring to seal the entry's bearer: set BATONDECK_MASTER_KEY", nil)
	}
	id := req.ID
	if id == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return resp, s.c.mapErr("register_wake_session", err)
		}
		id = "W-" + hex.EncodeToString(b)
	}
	agent := req.Agent
	if agent == "" {
		agent = AgentID(caller)
	}
	now := s.c.now()
	w := store.WakeSession{
		ID: id, IdentityID: caller.Identity, AgentID: agent, SocketPath: req.SocketPath,
		LastCursor: req.Cursor, CreatedAt: now.UnixMilli(), LastSeenAt: now.UnixMilli(), ExpiresAt: now.Add(s.c.wakeTTL).UnixMilli(),
		PID: req.PID, Role: req.Role, ClientSessionID: req.ClientSessionID, ProcStart: req.ProcStart,
	}
	if w.BearerSealed, err = s.c.sealer.Encrypt([]byte(req.Bearer), BearerAAD(w.ID)); err != nil {
		return resp, s.c.mapErr("register_wake_session", err)
	}
	if req.SocketToken != "" {
		if w.SocketTokenSealed, err = s.c.sealer.Encrypt([]byte(req.SocketToken), SocketTokenAAD(w.ID)); err != nil {
			return resp, s.c.mapErr("register_wake_session", err)
		}
	}
	if err := s.c.store.PutWakeSession(ctx, w); err != nil {
		return resp, s.c.mapErr("register_wake_session", err)
	}
	return RegisterWakeSessionResp{ID: w.ID, AgentID: agent, ExpiresAt: isoMs(w.ExpiresAt)}, nil
}

// ListWakeSessions opens each row's bearer and socket token under the row's own AAD.
func (s sessions) ListWakeSessions(ctx context.Context) (out []WakeSessionRow, err error) {
	defer s.c.recovered("list_wake_sessions", &err)
	rows, err := s.c.store.ListWakeSessions(ctx)
	if err != nil {
		return nil, s.c.mapErr("list_wake_sessions", err)
	}
	for _, w := range rows {
		r := WakeSessionRow{ID: w.ID, IdentityID: w.IdentityID, AgentID: w.AgentID, PID: w.PID, Role: w.Role,
			SocketPath: w.SocketPath, ClientSessionID: w.ClientSessionID, ProcStart: w.ProcStart, LastCursor: w.LastCursor, CreatedAt: w.CreatedAt}
		if s.c.sealer != nil && w.BearerSealed != nil {
			if pt, err := s.c.sealer.Decrypt(w.BearerSealed, BearerAAD(w.ID)); err == nil {
				r.Bearer, r.Sealed = string(pt), true
			}
			if w.SocketTokenSealed != nil {
				if pt, err := s.c.sealer.Decrypt(w.SocketTokenSealed, SocketTokenAAD(w.ID)); err == nil {
					r.SocketToken = string(pt)
				} else {
					r.Bearer, r.Sealed = "", false
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// SetWakeSessionCursor is one statement.
func (s sessions) SetWakeSessionCursor(ctx context.Context, id string, cursor int64) (err error) {
	defer s.c.recovered("set_wake_session_cursor", &err)
	if err := s.c.store.SetWakeSessionCursor(ctx, id, cursor, s.c.nowMs()); err != nil {
		return s.c.mapErr("set_wake_session_cursor", err)
	}
	return nil
}

// DropWakeSession is one statement; NOT_FOUND is success.
func (s sessions) DropWakeSession(ctx context.Context, id string) (err error) {
	defer s.c.recovered("drop_wake_session", &err)
	err = s.c.store.DeleteWakeSession(ctx, id)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return nil
	}
	if err != nil {
		return s.c.mapErr("drop_wake_session", err)
	}
	return nil
}

// WakeScopes is the token scopes a caller's doorbell listener subscribes to: its own (the assigned
// doorbells naming it) and BroadcastScope of every project it is a member of, read once (one
// ListProjects). A membership gained after the read is not heard until the listener reconnects.
func (c *Core) WakeScopes(ctx context.Context, caller domain.Caller) ([]string, error) {
	if caller.TokenScope == "" {
		return nil, internalf("wake scopes: the caller has no token scope")
	}
	mine, err := c.store.ListProjects(ctx, caller.Identity)
	if err != nil {
		return nil, c.mapErr("wake_scopes", err)
	}
	out := make([]string, 0, 1+len(mine))
	out = append(out, caller.TokenScope)
	for _, pm := range mine {
		out = append(out, BroadcastScope(pm.Project.ID))
	}
	return out, nil
}
