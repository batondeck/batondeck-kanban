package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The capability grants area of contract_grants.go, on both engines.

type grantQueries interface {
	GetCapabilityGrant(ctx context.Context, arg sqlitedb.GetCapabilityGrantParams) (sqlitedb.CapabilityGrant, error)
	UpsertCapabilityGrant(ctx context.Context, arg sqlitedb.UpsertCapabilityGrantParams) (sqlitedb.CapabilityGrant, error)
	DeleteCapabilityGrant(ctx context.Context, arg sqlitedb.DeleteCapabilityGrantParams) (int64, error)
	ListMemberGrants(ctx context.Context, identityID string) ([]sqlitedb.ListMemberGrantsRow, error)
}

func (s *SQLite) SetGrant(ctx context.Context, in SetGrantInput) (Grant, error) {
	return setGrant(ctx, s, in)
}

func (s *Postgres) SetGrant(ctx context.Context, in SetGrantInput) (Grant, error) {
	return setGrant(ctx, s, in)
}

func (s *SQLite) GetGrant(ctx context.Context, projectID, identityID string) (Grant, error) {
	return getGrant(ctx, s, projectID, identityID)
}

func (s *Postgres) GetGrant(ctx context.Context, projectID, identityID string) (Grant, error) {
	return getGrant(ctx, s, projectID, identityID)
}

func (s *SQLite) DeleteGrant(ctx context.Context, projectID, identityID string) (bool, error) {
	return deleteGrant(ctx, s, projectID, identityID)
}

func (s *Postgres) DeleteGrant(ctx context.Context, projectID, identityID string) (bool, error) {
	return deleteGrant(ctx, s, projectID, identityID)
}

func (s *SQLite) ListMemberGrants(ctx context.Context, identityID string) ([]MemberGrant, error) {
	return listMemberGrants(ctx, s, identityID)
}

func (s *Postgres) ListMemberGrants(ctx context.Context, identityID string) ([]MemberGrant, error) {
	return listMemberGrants(ctx, s, identityID)
}

func grantOf(r sqlitedb.CapabilityGrant) (Grant, error) {
	g := Grant{ProjectID: r.ProjectID, IdentityID: r.IdentityID, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
	if err := json.Unmarshal([]byte(r.GrantJson), &g.Grant); err != nil {
		return Grant{}, fmt.Errorf("store: grant json: %w", err)
	}
	return g, nil
}

func setGrant(ctx context.Context, e engine, in SetGrantInput) (Grant, error) {
	if err := CheckGrantInput(in); err != nil {
		return Grant{}, err
	}
	b, err := json.Marshal(in.Grant)
	if err != nil {
		return Grant{}, fmt.Errorf("store: grant json: %w", err)
	}
	row, err := e.queries(true).UpsertCapabilityGrant(ctx, sqlitedb.UpsertCapabilityGrantParams{
		ProjectID: in.ProjectID, IdentityID: in.IdentityID, GrantJson: string(b), UpdatedBy: in.UpdatedBy, UpdatedAt: in.Now,
	})
	switch {
	case isForeignKeyViolation(err):
		return Grant{}, domain.NotFound("Member", in.IdentityID)
	case err != nil:
		return Grant{}, fmt.Errorf("store: set grant: %w", err)
	}
	return grantOf(row)
}

func getGrant(ctx context.Context, e engine, projectID, identityID string) (Grant, error) {
	row, err := e.queries(false).GetCapabilityGrant(ctx, sqlitedb.GetCapabilityGrantParams{ProjectID: projectID, IdentityID: identityID})
	if err := found(err, "Grant", identityID); err != nil {
		return Grant{}, err
	}
	return grantOf(row)
}

func deleteGrant(ctx context.Context, e engine, projectID, identityID string) (bool, error) {
	n, err := e.queries(true).DeleteCapabilityGrant(ctx, sqlitedb.DeleteCapabilityGrantParams{ProjectID: projectID, IdentityID: identityID})
	if err != nil {
		return false, fmt.Errorf("store: delete grant: %w", err)
	}
	return n > 0, nil
}

func listMemberGrants(ctx context.Context, e engine, identityID string) ([]MemberGrant, error) {
	rows, err := e.queries(false).ListMemberGrants(ctx, identityID)
	if err != nil {
		return nil, fmt.Errorf("store: list member grants: %w", err)
	}
	out := make([]MemberGrant, 0, len(rows))
	for _, r := range rows {
		m := MemberGrant{ProjectID: r.ProjectID, Role: domain.Role(r.Role)}
		if r.GrantJson.Valid {
			var g domain.CapabilityGrant
			if err := json.Unmarshal([]byte(r.GrantJson.String), &g); err != nil {
				return nil, fmt.Errorf("store: grant json: %w", err)
			}
			m.Grant = &g
		}
		out = append(out, m)
	}
	return out, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) GetCapabilityGrant(ctx context.Context, arg sqlitedb.GetCapabilityGrantParams) (sqlitedb.CapabilityGrant, error) {
	r, err := p.q.GetCapabilityGrant(ctx, pgdb.GetCapabilityGrantParams(arg))
	return sqlitedb.CapabilityGrant(r), err
}

func (p pgQueries) UpsertCapabilityGrant(ctx context.Context, arg sqlitedb.UpsertCapabilityGrantParams) (sqlitedb.CapabilityGrant, error) {
	r, err := p.q.UpsertCapabilityGrant(ctx, pgdb.UpsertCapabilityGrantParams(arg))
	return sqlitedb.CapabilityGrant(r), err
}

func (p pgQueries) DeleteCapabilityGrant(ctx context.Context, arg sqlitedb.DeleteCapabilityGrantParams) (int64, error) {
	return p.q.DeleteCapabilityGrant(ctx, pgdb.DeleteCapabilityGrantParams(arg))
}

func (p pgQueries) ListMemberGrants(ctx context.Context, identityID string) ([]sqlitedb.ListMemberGrantsRow, error) {
	rows, err := p.q.ListMemberGrants(ctx, identityID)
	return convert(rows, func(r pgdb.ListMemberGrantsRow) sqlitedb.ListMemberGrantsRow { return sqlitedb.ListMemberGrantsRow(r) }), err
}
