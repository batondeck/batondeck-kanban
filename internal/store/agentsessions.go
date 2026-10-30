package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The agent-session area of contract_agentsessions.go, on both engines. The upsert and the touch
// are one statement each, the touch's per-tool count included, so concurrent touches of one row
// never lose a count.

type agentSessionQueries interface {
	UpsertAgentSession(ctx context.Context, arg sqlitedb.UpsertAgentSessionParams) (sqlitedb.UpsertAgentSessionRow, error)
	TouchAgentSession(ctx context.Context, arg sqlitedb.TouchAgentSessionParams) error
	DeleteAgentSession(ctx context.Context, arg sqlitedb.DeleteAgentSessionParams) (int64, error)
	ListAgentSessions(ctx context.Context, arg sqlitedb.ListAgentSessionsParams) ([]sqlitedb.ListAgentSessionsRow, error)
	DeleteIdleAgentSessions(ctx context.Context, arg sqlitedb.DeleteIdleAgentSessionsParams) (int64, error)
}

func (s *SQLite) UpsertAgentSession(ctx context.Context, in AgentSessionStart) (AgentSession, error) {
	return upsertAgentSession(ctx, s, in)
}

func (s *Postgres) UpsertAgentSession(ctx context.Context, in AgentSessionStart) (AgentSession, error) {
	return upsertAgentSession(ctx, s, in)
}

func (s *SQLite) TouchAgentSession(ctx context.Context, in AgentSessionTouch) error {
	return touchAgentSession(ctx, s, in)
}

func (s *Postgres) TouchAgentSession(ctx context.Context, in AgentSessionTouch) error {
	return touchAgentSession(ctx, s, in)
}

func (s *SQLite) DeleteAgentSession(ctx context.Context, identityID, agentID string) error {
	return deleteAgentSession(ctx, s, identityID, agentID)
}

func (s *Postgres) DeleteAgentSession(ctx context.Context, identityID, agentID string) error {
	return deleteAgentSession(ctx, s, identityID, agentID)
}

func (s *SQLite) ListAgentSessions(ctx context.Context, identityID string, limit int) ([]AgentSession, error) {
	return listAgentSessions(ctx, s, identityID, limit)
}

func (s *Postgres) ListAgentSessions(ctx context.Context, identityID string, limit int) ([]AgentSession, error) {
	return listAgentSessions(ctx, s, identityID, limit)
}

func (s *SQLite) DeleteIdleAgentSessions(ctx context.Context, before int64, limit int, keep []AgentSessionKey) (int64, error) {
	return deleteIdleAgentSessions(ctx, s, before, limit, keep)
}

func (s *Postgres) DeleteIdleAgentSessions(ctx context.Context, before int64, limit int, keep []AgentSessionKey) (int64, error) {
	return deleteIdleAgentSessions(ctx, s, before, limit, keep)
}

func validAgent(identityID, agentID, agentName, kind string) error {
	if identityID == "" || agentID == "" || agentName == "" || kind == "" {
		return domain.Validation("an agent session needs an identity, an agent, a name and a kind", nil)
	}
	return nil
}

func upsertAgentSession(ctx context.Context, e engine, in AgentSessionStart) (AgentSession, error) {
	if err := validAgent(in.IdentityID, in.AgentID, in.AgentName, in.Kind); err != nil {
		return AgentSession{}, err
	}
	r, err := e.queries(true).UpsertAgentSession(ctx, sqlitedb.UpsertAgentSessionParams{
		IdentityID: in.IdentityID, AgentID: in.AgentID, AgentName: in.AgentName, Kind: in.Kind,
		ClientName: nullText(in.ClientName), ClientVersion: nullText(in.ClientVersion), OauthClientID: nullText(in.OAuthClientID), At: in.At,
	})
	if err != nil {
		return AgentSession{}, fmt.Errorf("store: agent session: %w", err)
	}
	return agentSessionFrom(sqlitedb.ListAgentSessionsRow(r))
}

func touchAgentSession(ctx context.Context, e engine, in AgentSessionTouch) error {
	if err := validAgent(in.IdentityID, in.AgentID, in.AgentName, in.Kind); err != nil {
		return err
	}
	if in.Tool == "" || in.Count < 0 {
		return domain.Validation("a touch needs a tool and a count of 0 or more", map[string]any{"count": in.Count})
	}
	err := e.queries(true).TouchAgentSession(ctx, sqlitedb.TouchAgentSessionParams{
		IdentityID: in.IdentityID, AgentID: in.AgentID, AgentName: in.AgentName, Kind: in.Kind, At: in.At,
		N: max(in.Count, 1), Tool: in.Tool, ProjectID: nullText(in.ProjectID),
	})
	if err != nil {
		return fmt.Errorf("store: touch agent session: %w", err)
	}
	return nil
}

func deleteAgentSession(ctx context.Context, e engine, identityID, agentID string) error {
	n, err := e.queries(true).DeleteAgentSession(ctx, sqlitedb.DeleteAgentSessionParams{IdentityID: identityID, AgentID: agentID})
	if err != nil {
		return fmt.Errorf("store: delete agent session: %w", err)
	}
	if n == 0 {
		return domain.NotFound("AgentSession", agentID)
	}
	return nil
}

func listAgentSessions(ctx context.Context, e engine, identityID string, limit int) ([]AgentSession, error) {
	rows, err := e.queries(false).ListAgentSessions(ctx, sqlitedb.ListAgentSessionsParams{IdentityID: identityID, PageLimit: int64(clamp(limit, 100, 1000))})
	if err != nil {
		return nil, fmt.Errorf("store: list agent sessions: %w", err)
	}
	out := make([]AgentSession, 0, len(rows))
	for _, r := range rows {
		s, err := agentSessionFrom(r)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func deleteIdleAgentSessions(ctx context.Context, e engine, before int64, limit int, keep []AgentSessionKey) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	pairs := make([][2]string, len(keep)) // the queries read keep as [[identity_id, agent_id], ...]
	for i, k := range keep {
		pairs[i] = [2]string{k.IdentityID, k.AgentID}
	}
	kept, err := json.Marshal(pairs)
	if err != nil {
		return 0, fmt.Errorf("store: delete idle agent sessions: %w", err)
	}
	n, err := e.queries(true).DeleteIdleAgentSessions(ctx, sqlitedb.DeleteIdleAgentSessionsParams{Keep: string(kept), Before: before, PageLimit: int64(limit)})
	if err != nil {
		return 0, fmt.Errorf("store: delete idle agent sessions: %w", err)
	}
	return n, nil
}

func agentSessionFrom(r sqlitedb.ListAgentSessionsRow) (AgentSession, error) {
	counts := map[string]int64{}
	if err := json.Unmarshal([]byte(r.ToolCounts), &counts); err != nil {
		return AgentSession{}, fmt.Errorf("store: agent session %s/%s tool counts: %w", r.IdentityID, r.AgentID, err)
	}
	if counts == nil {
		counts = map[string]int64{}
	}
	return AgentSession{
		IdentityID: r.IdentityID, AgentID: r.AgentID, AgentName: r.AgentName, Kind: r.Kind,
		ClientName: r.ClientName.String, ClientVersion: r.ClientVersion.String, OAuthClientID: r.OauthClientID.String,
		FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt, SessionCount: r.SessionCount, RequestCount: r.RequestCount,
		ToolCounts: counts, LastTool: r.LastTool.String, LastProjectID: r.LastProjectID.String,
	}, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) UpsertAgentSession(ctx context.Context, arg sqlitedb.UpsertAgentSessionParams) (sqlitedb.UpsertAgentSessionRow, error) {
	r, err := p.q.UpsertAgentSession(ctx, pgdb.UpsertAgentSessionParams(arg))
	return sqlitedb.UpsertAgentSessionRow(r), err
}

func (p pgQueries) TouchAgentSession(ctx context.Context, arg sqlitedb.TouchAgentSessionParams) error {
	return p.q.TouchAgentSession(ctx, pgdb.TouchAgentSessionParams(arg))
}

func (p pgQueries) DeleteAgentSession(ctx context.Context, arg sqlitedb.DeleteAgentSessionParams) (int64, error) {
	return p.q.DeleteAgentSession(ctx, pgdb.DeleteAgentSessionParams(arg))
}

func (p pgQueries) ListAgentSessions(ctx context.Context, arg sqlitedb.ListAgentSessionsParams) ([]sqlitedb.ListAgentSessionsRow, error) {
	rows, err := p.q.ListAgentSessions(ctx, pgdb.ListAgentSessionsParams(arg))
	return convert(rows, func(r pgdb.ListAgentSessionsRow) sqlitedb.ListAgentSessionsRow {
		return sqlitedb.ListAgentSessionsRow(r)
	}), err
}

func (p pgQueries) DeleteIdleAgentSessions(ctx context.Context, arg sqlitedb.DeleteIdleAgentSessionsParams) (int64, error) {
	return p.q.DeleteIdleAgentSessions(ctx, pgdb.DeleteIdleAgentSessionsParams(arg))
}
