package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Sessions is the agent and wake session bookkeeping. The MCP transport touches the caller's agent
// session on every request and drops it when the session ends; /wake registers a wake session.
// None of these writes an event, an audit row or a doorbell.
type Sessions interface {
	// TouchAgentSession records one request from the caller's agent session (identity and agent from
	// the caller): first seen, last seen, the request and per-tool counts, the client.
	TouchAgentSession(ctx context.Context, caller domain.Caller, req TouchAgentSessionReq) (TouchAgentSessionResp, error)

	// RemoveAgentSession drops the caller's agent session when its MCP session ends or is collected.
	RemoveAgentSession(ctx context.Context, caller domain.Caller, req RemoveAgentSessionReq) (RemoveAgentSessionResp, error)

	// ListAgentSessions is list_agent_sessions.
	// Not tenant-scoped; workspace admin for every identity, else the caller's own. Writes nothing.
	ListAgentSessions(ctx context.Context, caller domain.Caller, req ListAgentSessionsReq) (ListAgentSessionsResp, error)

	// RegisterWakeSession records the caller's /wake attachment, where its doorbells are delivered.
	RegisterWakeSession(ctx context.Context, caller domain.Caller, req RegisterWakeSessionReq) (RegisterWakeSessionResp, error)
}

// TouchAgentSessionReq is one request seen on an agent session.
type TouchAgentSessionReq struct {
	SessionKey    string      `json:"sessionKey"`
	AgentName     string      `json:"agentName"`
	Kind          SessionKind `json:"kind"`
	ClientName    string      `json:"clientName,omitzero"`
	ClientVersion string      `json:"clientVersion,omitzero"`
	OAuthClientID string      `json:"oauthClientId,omitzero"`
	Tool          string      `json:"tool,omitzero"`
	ProjectID     string      `json:"projectId,omitzero"`
}

// TouchAgentSessionResp is empty.
type TouchAgentSessionResp struct{}

// RemoveAgentSessionReq names the session that ended.
type RemoveAgentSessionReq struct {
	SessionKey string `json:"sessionKey"`
}

// RemoveAgentSessionResp is empty.
type RemoveAgentSessionResp struct{}

// RegisterWakeSessionReq is a /wake attachment: the socket the doorbell is delivered on and the
// change-log cursor the caller has seen.
type RegisterWakeSessionReq struct {
	SocketPath string `json:"socketPath,omitzero"`
	Cursor     int64  `json:"cursor,omitzero"`
}

// RegisterWakeSessionResp is the registered wake session.
type RegisterWakeSessionResp struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expiresAt"`
}

// ListAgentSessionsReq is list_agent_sessions's input.
type ListAgentSessionsReq struct {
	All   bool  `json:"all,omitzero"`
	Limit int64 `json:"limit,omitzero"`
}

// ListAgentSessionsResp is list_agent_sessions's output.
type ListAgentSessionsResp struct {
	Sessions []AgentSession `json:"sessions"`
}

// AgentSession is list_agent_sessions's output sessions[], first seen there.
type AgentSession struct {
	AgentID         string             `json:"agentId"`
	AgentName       string             `json:"agentName"`
	FirstSeenAt     string             `json:"firstSeenAt"`
	IdentityID      string             `json:"identityId"`
	Kind            SessionKind        `json:"kind"`
	LastSeenAt      string             `json:"lastSeenAt"`
	RequestCount    float64            `json:"requestCount"`
	SessionCount    float64            `json:"sessionCount"`
	SessionKey      string             `json:"sessionKey"`
	ToolCounts      map[string]float64 `json:"toolCounts"`
	ClientName      string             `json:"clientName,omitzero"`
	ClientVersion   string             `json:"clientVersion,omitzero"`
	IdentityDisplay *string            `json:"identityDisplay,omitzero"`
	LastProjectID   string             `json:"lastProjectId,omitzero"`
	LastTool        string             `json:"lastTool,omitzero"`
	OAuthClientFP   string             `json:"oauthClientFp,omitzero"`
	RevokedAt       string             `json:"revokedAt,omitzero"`
	RevokedBy       string             `json:"revokedBy,omitzero"`
	TenantID        string             `json:"tenantId,omitzero"`
	WakeAttachedAt  string             `json:"wakeAttachedAt,omitzero"`
	WakeBound       bool               `json:"wakeBound,omitzero"`
}

// SessionKind is how an agent session authenticated.
type SessionKind string

// SessionKindValues are SessionKind's values, in the schema's order.
var SessionKindValues = []SessionKind{"dev", "oauth_user", "service_account"}
