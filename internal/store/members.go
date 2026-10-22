package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

type memberQueries interface {
	UpsertProjectMember(ctx context.Context, arg sqlitedb.UpsertProjectMemberParams) (sqlitedb.ProjectMember, error)
	GetProjectMember(ctx context.Context, arg sqlitedb.GetProjectMemberParams) (sqlitedb.ProjectMember, error)
	ListProjectMembers(ctx context.Context, projectID string) ([]sqlitedb.ProjectMember, error)
	DeleteProjectMember(ctx context.Context, arg sqlitedb.DeleteProjectMemberParams) (int64, error)
}

func (s *SQLite) AddMember(ctx context.Context, projectID, identityID string, role domain.Role, addedBy string) (Member, error) {
	return addMember(ctx, s, projectID, identityID, role, addedBy)
}

func (s *Postgres) AddMember(ctx context.Context, projectID, identityID string, role domain.Role, addedBy string) (Member, error) {
	return addMember(ctx, s, projectID, identityID, role, addedBy)
}

func (s *SQLite) RemoveMember(ctx context.Context, projectID, identityID string) error {
	return removeMember(ctx, s, projectID, identityID)
}

func (s *Postgres) RemoveMember(ctx context.Context, projectID, identityID string) error {
	return removeMember(ctx, s, projectID, identityID)
}

func (s *SQLite) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	return listMembers(ctx, s, projectID)
}

func (s *Postgres) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	return listMembers(ctx, s, projectID)
}

func (s *SQLite) GetMember(ctx context.Context, projectID, identityID string) (Member, error) {
	return getMember(ctx, s, projectID, identityID)
}

func (s *Postgres) GetMember(ctx context.Context, projectID, identityID string) (Member, error) {
	return getMember(ctx, s, projectID, identityID)
}

func addMember(ctx context.Context, e engine, projectID, identityID string, role domain.Role, addedBy string) (Member, error) {
	if _, err := domain.ParseRole(string(role)); err != nil {
		return Member{}, err
	}
	if identityID == "" || addedBy == "" {
		return Member{}, domain.Validation("a member needs an identity and who added it", nil)
	}
	row, err := e.queries(true).UpsertProjectMember(ctx, sqlitedb.UpsertProjectMemberParams{
		ProjectID: projectID, IdentityID: identityID, Role: string(role), AddedBy: addedBy, AddedAt: nowMillis(),
	})
	switch {
	case isForeignKeyViolation(err):
		return Member{}, domain.NotFound("Project", projectID)
	case err != nil:
		return Member{}, fmt.Errorf("store: add member: %w", err)
	}
	return memberFromRow(row), nil
}

func removeMember(ctx context.Context, e engine, projectID, identityID string) error {
	q := e.queries(true)
	n, err := q.DeleteProjectMember(ctx, sqlitedb.DeleteProjectMemberParams{ProjectID: projectID, IdentityID: identityID})
	if err != nil {
		return fmt.Errorf("store: remove member: %w", err)
	}
	if n == 0 {
		if _, err := q.GetProject(ctx, projectID); err != nil {
			return found(err, "Project", projectID)
		}
		return domain.NotFound("Member", identityID)
	}
	return nil
}

func listMembers(ctx context.Context, e engine, projectID string) ([]Member, error) {
	q := e.queries(false)
	rows, err := q.ListProjectMembers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list members: %w", err)
	}
	if len(rows) == 0 {
		// No member: a missing project, which is NOT_FOUND, or a project left with none.
		if _, err := q.GetProject(ctx, projectID); err != nil {
			return nil, found(err, "Project", projectID)
		}
	}
	return convert(rows, memberFromRow), nil
}

func getMember(ctx context.Context, e engine, projectID, identityID string) (Member, error) {
	row, err := e.queries(false).GetProjectMember(ctx, sqlitedb.GetProjectMemberParams{ProjectID: projectID, IdentityID: identityID})
	if err := found(err, "Member", identityID); err != nil {
		return Member{}, err
	}
	return memberFromRow(row), nil
}

func memberFromRow(r sqlitedb.ProjectMember) Member {
	return Member{ProjectID: r.ProjectID, IdentityID: r.IdentityID, Role: domain.Role(r.Role), AddedBy: r.AddedBy, AddedAt: r.AddedAt}
}

func (p pgQueries) UpsertProjectMember(ctx context.Context, arg sqlitedb.UpsertProjectMemberParams) (sqlitedb.ProjectMember, error) {
	r, err := p.q.UpsertProjectMember(ctx, pgdb.UpsertProjectMemberParams(arg))
	return sqlitedb.ProjectMember(r), err
}

func (p pgQueries) GetProjectMember(ctx context.Context, arg sqlitedb.GetProjectMemberParams) (sqlitedb.ProjectMember, error) {
	r, err := p.q.GetProjectMember(ctx, pgdb.GetProjectMemberParams(arg))
	return sqlitedb.ProjectMember(r), err
}

func (p pgQueries) ListProjectMembers(ctx context.Context, projectID string) ([]sqlitedb.ProjectMember, error) {
	rows, err := p.q.ListProjectMembers(ctx, projectID)
	return convert(rows, func(r pgdb.ProjectMember) sqlitedb.ProjectMember { return sqlitedb.ProjectMember(r) }), err
}

func (p pgQueries) DeleteProjectMember(ctx context.Context, arg sqlitedb.DeleteProjectMemberParams) (int64, error) {
	return p.q.DeleteProjectMember(ctx, pgdb.DeleteProjectMemberParams(arg))
}
