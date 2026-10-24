package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The capability registry and skill stats area of contract_profiles.go, on both engines.

type profileQueries interface {
	UpsertAgentProfile(ctx context.Context, arg sqlitedb.UpsertAgentProfileParams) (sqlitedb.AgentProfile, error)
	GetAgentProfile(ctx context.Context, arg sqlitedb.GetAgentProfileParams) (sqlitedb.AgentProfile, error)
	RecordSkillOutcome(ctx context.Context, arg sqlitedb.RecordSkillOutcomeParams) error
	ListSkillStats(ctx context.Context, arg sqlitedb.ListSkillStatsParams) ([]sqlitedb.SkillStat, error)
}

func (s *SQLite) UpsertAgentProfile(ctx context.Context, in UpsertAgentProfileInput) (AgentProfile, error) {
	return upsertAgentProfile(ctx, s, in)
}

func (s *Postgres) UpsertAgentProfile(ctx context.Context, in UpsertAgentProfileInput) (AgentProfile, error) {
	return upsertAgentProfile(ctx, s, in)
}

func (s *SQLite) GetAgentProfile(ctx context.Context, projectID, agentKey string) (AgentProfile, error) {
	return getAgentProfile(ctx, s, projectID, agentKey)
}

func (s *Postgres) GetAgentProfile(ctx context.Context, projectID, agentKey string) (AgentProfile, error) {
	return getAgentProfile(ctx, s, projectID, agentKey)
}

func (s *SQLite) RecordSkillOutcome(ctx context.Context, in SkillOutcome) error {
	return recordSkillOutcome(ctx, s, in)
}

func (s *Postgres) RecordSkillOutcome(ctx context.Context, in SkillOutcome) error {
	return recordSkillOutcome(ctx, s, in)
}

func (s *SQLite) ListSkillStats(ctx context.Context, projectID, agentKey string) ([]SkillStat, error) {
	return listSkillStats(ctx, s, projectID, agentKey)
}

func (s *Postgres) ListSkillStats(ctx context.Context, projectID, agentKey string) ([]SkillStat, error) {
	return listSkillStats(ctx, s, projectID, agentKey)
}

func profileOf(r sqlitedb.AgentProfile) (AgentProfile, error) {
	p := AgentProfile{ProjectID: r.ProjectID, AgentKey: r.AgentKey, CostClass: strPtr(r.CostClass), MaxEffort: strPtr(r.MaxEffort),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.MaxConcurrency.Valid {
		v := r.MaxConcurrency.Int64
		p.MaxConcurrency = &v
	}
	var err error
	if p.Capabilities, err = readList(r.Capabilities); err != nil {
		return AgentProfile{}, fmt.Errorf("store: profile capabilities: %w", err)
	}
	if p.Tags, err = readList(r.Tags); err != nil {
		return AgentProfile{}, fmt.Errorf("store: profile tags: %w", err)
	}
	if p.Models, err = readList(r.Models); err != nil {
		return AgentProfile{}, fmt.Errorf("store: profile models: %w", err)
	}
	return p, nil
}

func upsertAgentProfile(ctx context.Context, e engine, in UpsertAgentProfileInput) (AgentProfile, error) {
	if err := CheckAgentProfile(in); err != nil {
		return AgentProfile{}, err
	}
	var maxc sql.NullInt64
	if in.MaxConcurrency != nil {
		maxc = sql.NullInt64{Int64: *in.MaxConcurrency, Valid: true}
	}
	row, err := e.queries(true).UpsertAgentProfile(ctx, sqlitedb.UpsertAgentProfileParams{
		AgentKey: in.AgentKey, Capabilities: jsonList(in.Capabilities), Tags: jsonList(in.Tags), MaxConcurrency: maxc,
		CostClass: nullStr(in.CostClass), Models: jsonList(in.Models), MaxEffort: nullStr(in.MaxEffort), CreatedBy: in.CreatedBy,
		Now: in.Now, ProjectID: in.ProjectID,
	})
	if err := found(err, "Project", in.ProjectID); err != nil {
		return AgentProfile{}, err
	}
	return profileOf(row)
}

func getAgentProfile(ctx context.Context, e engine, projectID, agentKey string) (AgentProfile, error) {
	row, err := e.queries(false).GetAgentProfile(ctx, sqlitedb.GetAgentProfileParams{ProjectID: projectID, AgentKey: agentKey})
	if err := found(err, "AgentProfile", agentKey); err != nil {
		return AgentProfile{}, err
	}
	return profileOf(row)
}

func recordSkillOutcome(ctx context.Context, e engine, in SkillOutcome) error {
	var completed, blocked int64 = 0, 1
	if in.Completed {
		completed, blocked = 1, 0
	}
	for _, c := range in.Capabilities {
		if err := e.queries(true).RecordSkillOutcome(ctx, sqlitedb.RecordSkillOutcomeParams{
			ProjectID: in.ProjectID, AgentKey: in.AgentKey, Capability: c, Completed: completed, Blocked: blocked,
			TotalMs: max(0, in.ElapsedMs), UpdatedAt: in.Now,
		}); err != nil {
			return fmt.Errorf("store: record skill outcome: %w", err)
		}
	}
	return nil
}

func listSkillStats(ctx context.Context, e engine, projectID, agentKey string) ([]SkillStat, error) {
	rows, err := e.queries(false).ListSkillStats(ctx, sqlitedb.ListSkillStatsParams{ProjectID: projectID, AgentKey: agentKey})
	if err != nil {
		return nil, fmt.Errorf("store: list skill stats: %w", err)
	}
	out := make([]SkillStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, SkillStat(r))
	}
	return out, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) UpsertAgentProfile(ctx context.Context, arg sqlitedb.UpsertAgentProfileParams) (sqlitedb.AgentProfile, error) {
	r, err := p.q.UpsertAgentProfile(ctx, pgdb.UpsertAgentProfileParams(arg))
	return sqlitedb.AgentProfile(r), err
}

func (p pgQueries) GetAgentProfile(ctx context.Context, arg sqlitedb.GetAgentProfileParams) (sqlitedb.AgentProfile, error) {
	r, err := p.q.GetAgentProfile(ctx, pgdb.GetAgentProfileParams(arg))
	return sqlitedb.AgentProfile(r), err
}

func (p pgQueries) RecordSkillOutcome(ctx context.Context, arg sqlitedb.RecordSkillOutcomeParams) error {
	return p.q.RecordSkillOutcome(ctx, pgdb.RecordSkillOutcomeParams(arg))
}

func (p pgQueries) ListSkillStats(ctx context.Context, arg sqlitedb.ListSkillStatsParams) ([]sqlitedb.SkillStat, error) {
	rows, err := p.q.ListSkillStats(ctx, pgdb.ListSkillStatsParams(arg))
	return convert(rows, func(r pgdb.SkillStat) sqlitedb.SkillStat { return sqlitedb.SkillStat(r) }), err
}
