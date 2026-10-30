package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// (store.TouchAgentSession). The transport's kind is SessionKindAPIToken or SessionKindOAuth.
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

// DisconnectAgent ends the live sessions before it deletes the row, so a tool call racing it cannot
// touch the row back into being on a session that is still open. The end of a session leaves the
// row (P-264), so the delete here is the one that removes it; a row already gone (the retention
// sweep's, or a concurrent disconnect's) is NOT_FOUND only when no live session was ended either.
func (s sessions) DisconnectAgent(ctx context.Context, caller domain.Caller, req DisconnectAgentReq) (resp DisconnectAgentResp, err error) {
	defer s.c.recovered("disconnect_agent", &err)
	if _, err := s.c.authorize(ctx, caller, "", "disconnect_agent", domain.GrantSubject{}, false); err != nil {
		return resp, err
	}
	switch {
	case req.IdentityID == "":
		return resp, domain.Validation("identityId is required", map[string]any{"field": "identityId"})
	case req.AgentID == "":
		return resp, domain.Validation("agentId is required", map[string]any{"field": "agentId"})
	case req.IdentityID != caller.Identity && !caller.WorkspaceAdmin:
		return resp, forbidden("You may only disconnect your own agents (workspace admin required for others)", map[string]any{"requiredCapability": "workspace-admin"})
	}
	ended := s.c.endAgentSessions(ctx, req.IdentityID, req.AgentID)
	err = s.c.store.DeleteAgentSession(ctx, req.IdentityID, req.AgentID)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		if ended == 0 {
			return resp, err
		}
		err = nil
	}
	if err != nil {
		return resp, s.c.mapErr("disconnect_agent", err)
	}
	d, _ := domain.DescribeAudit("disconnect_agent", map[string]any{"identityId": req.IdentityID, "agentId": req.AgentID})
	data, err := json.Marshal(d.Data)
	if err != nil {
		return resp, s.c.mapErr("disconnect_agent", err)
	}
	if _, err := s.c.store.AppendAudit(ctx, store.AuditRow{TS: s.c.nowMs(), Action: d.Action, Actor: caller.Identity, Agent: caller.Agent,
		Subject: d.Subject, Result: store.AuditOK, Data: data}); err != nil {
		return resp, s.c.mapErr("disconnect_agent", err)
	}
	return DisconnectAgentResp{Disconnected: true}, nil
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
	if err := s.CheckWakeAgent(ctx, caller, agent); err != nil {
		return resp, err
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

// CheckWakeAgent refuses, FORBIDDEN, an entry agent that is another agent's label for caller by
// ownLabel's own test (labelRefused), with no master pass: an entry has no project, and a label
// has no admin override (P-322; REVIEW-257 R2-Q1, R2-Q2). The entry would otherwise be rung on
// AgentScope(agent) and told to claim_next for a label whose claims are refused to it (P-276's
// line), and alias a mention of the label (P-343). An "id:<identity>" agent is the id AgentID
// derives for a caller that names none, so it is the caller's own or no one's: "id:" followed by
// anything but the caller's identity is FORBIDDEN, whatever named it (--agent, a token label, an
// OAuth client_name), and is never a label (P-345). A name no token carries and the caller's own
// token's label pass. Not tenant-scoped; one read of every token row for a non-"id:" agent.
func (s sessions) CheckWakeAgent(ctx context.Context, caller domain.Caller, agent string) error {
	if id, ok := strings.CutPrefix(agent, "id:"); ok {
		if id != caller.Identity {
			return forbidden("agent names another identity: an id: agent is only the caller's own, id:<its identity>",
				map[string]any{"field": "agent", "agent": agent})
		}
		return nil
	}
	refused, err := s.c.labelRefused(ctx, s.c.store, caller, agent)
	if err != nil {
		return s.c.mapErr("register_wake_session", err)
	}
	if refused {
		return forbidden("agent is another agent's label: a session registers only under its own token's label or a name no other account's token and no live token of its own carries; another account's label, or a live label of its own account on a token that does not carry it, is refused",
			map[string]any{"field": "agent", "label": agent})
	}
	return nil
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
// doorbells naming it) and BroadcastScope of every project it is a member of (one ListProjects).
// The device directory (internal/wake) reads it at register and re-reads it for every entry on each
// Sweep and whenever it reads an add_member, remove_member, create_project or delete_project change,
// so a membership is heard from its own change on (P-229, P-331).
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
