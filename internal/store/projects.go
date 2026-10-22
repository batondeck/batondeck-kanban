package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

type projectQueries interface {
	CreateProject(ctx context.Context, arg sqlitedb.CreateProjectParams) error
	GetProject(ctx context.Context, id string) (sqlitedb.Project, error)
	LockProject(ctx context.Context, id string) (sqlitedb.Project, error)
	ListProjectsForIdentity(ctx context.Context, identityID string) ([]sqlitedb.ListProjectsForIdentityRow, error)
	UpdateProjectSettings(ctx context.Context, arg sqlitedb.UpdateProjectSettingsParams) error
	DeleteProject(ctx context.Context, id string) (int64, error)
}

func (s *SQLite) CreateProject(ctx context.Context, in CreateProjectInput, opts WriteOptions) (Project, error) {
	return createProject(ctx, s, in, opts)
}

func (s *Postgres) CreateProject(ctx context.Context, in CreateProjectInput, opts WriteOptions) (Project, error) {
	return createProject(ctx, s, in, opts)
}

func (s *SQLite) GetProject(ctx context.Context, projectID string) (Project, error) {
	return getProject(ctx, s, projectID)
}

func (s *Postgres) GetProject(ctx context.Context, projectID string) (Project, error) {
	return getProject(ctx, s, projectID)
}

func (s *SQLite) ListProjects(ctx context.Context, identityID string) ([]ProjectMembership, error) {
	return listProjects(ctx, s, identityID)
}

func (s *Postgres) ListProjects(ctx context.Context, identityID string) ([]ProjectMembership, error) {
	return listProjects(ctx, s, identityID)
}

func (s *SQLite) UpdateSettings(ctx context.Context, projectID string, patch SettingsPatch) (Project, error) {
	return updateSettings(ctx, s, projectID, patch)
}

func (s *Postgres) UpdateSettings(ctx context.Context, projectID string, patch SettingsPatch) (Project, error) {
	return updateSettings(ctx, s, projectID, patch)
}

func (s *SQLite) DeleteProjectRecursive(ctx context.Context, projectID string) error {
	return deleteProjectRecursive(ctx, s, projectID)
}

func (s *Postgres) DeleteProjectRecursive(ctx context.Context, projectID string) error {
	return deleteProjectRecursive(ctx, s, projectID)
}

// newProjectSettings is what conductor's createProject writes besides reviewEnabled (true, a
// column here): quotaEnabled false and txnCap null.
const newProjectSettings = `{"quotaEnabled":false,"txnCap":null}`

func createProject(ctx context.Context, e engine, in CreateProjectInput, opts WriteOptions) (Project, error) {
	if strings.TrimSpace(in.Name) == "" || in.CreatedBy == "" {
		return Project{}, domain.Validation("a project needs a name and a creator", nil)
	}
	return idempotent(ctx, e, GlobalScope, opts, func(tx Store) (Project, error) {
		q := on(tx).queries(true)
		row := sqlitedb.Project{
			ID: newID("P-", 12), Name: in.Name, Settings: newProjectSettings, ReviewEnabled: 1,
			CreatedBy: in.CreatedBy, CreatedAt: nowMillis(),
		}
		if err := q.CreateProject(ctx, sqlitedb.CreateProjectParams(row)); err != nil {
			return Project{}, fmt.Errorf("store: create project: %w", err)
		}
		if _, err := q.UpsertProjectMember(ctx, sqlitedb.UpsertProjectMemberParams{
			ProjectID: row.ID, IdentityID: in.CreatedBy, Role: string(domain.RoleAdmin),
			AddedBy: in.CreatedBy, AddedAt: row.CreatedAt,
		}); err != nil {
			return Project{}, fmt.Errorf("store: create project: %w", err)
		}
		return projectFromRow(row)
	})
}

func getProject(ctx context.Context, e engine, projectID string) (Project, error) {
	row, err := e.queries(false).GetProject(ctx, projectID)
	if err := found(err, "Project", projectID); err != nil {
		return Project{}, err
	}
	return projectFromRow(row)
}

func listProjects(ctx context.Context, e engine, identityID string) ([]ProjectMembership, error) {
	rows, err := e.queries(false).ListProjectsForIdentity(ctx, identityID)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	out := make([]ProjectMembership, 0, len(rows))
	for _, r := range rows {
		p, err := projectFromRow(sqlitedb.Project{
			ID: r.ID, Name: r.Name, Settings: r.Settings, ReviewEnabled: r.ReviewEnabled,
			RetryPolicy: r.RetryPolicy, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ProjectMembership{Project: p, Role: domain.Role(r.Role)})
	}
	return out, nil
}

func updateSettings(ctx context.Context, e engine, projectID string, patch SettingsPatch) (Project, error) {
	if _, ok := patch["transitionTable"]; ok {
		return Project{}, domain.Validation("the transition table is fixed: settings take no transitionTable", map[string]any{"key": "transitionTable"})
	}
	var out Project
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		row, err := q.LockProject(ctx, projectID)
		if err := found(err, "Project", projectID); err != nil {
			return err
		}
		other := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(row.Settings), &other); err != nil {
			return fmt.Errorf("store: project %s settings: %w", projectID, err)
		}
		for k, v := range patch {
			isNull := bytes.Equal(bytes.TrimSpace(v), []byte("null"))
			switch k {
			case "reviewEnabled":
				var b bool
				if isNull || json.Unmarshal(v, &b) != nil {
					return domain.Validation("reviewEnabled must be a boolean", map[string]any{"key": k})
				}
				row.ReviewEnabled = 0
				if b {
					row.ReviewEnabled = 1
				}
			case "retryPolicy":
				if isNull {
					row.RetryPolicy = sql.NullString{}
					continue
				}
				var p domain.RetryPolicy
				d := json.NewDecoder(bytes.NewReader(v))
				d.DisallowUnknownFields()
				if err := d.Decode(&p); err != nil {
					return domain.Validation("retryPolicy must be a retry policy object", map[string]any{"key": k})
				}
				canon, _ := json.Marshal(p)
				row.RetryPolicy = sql.NullString{String: string(canon), Valid: true}
			default:
				if isNull {
					delete(other, k)
				} else {
					other[k] = v
				}
			}
		}
		settings, err := json.Marshal(other)
		if err != nil {
			return domain.Validation("a setting is not valid JSON", nil)
		}
		row.Settings = string(settings)
		if err := q.UpdateProjectSettings(ctx, sqlitedb.UpdateProjectSettingsParams{
			Settings: row.Settings, ReviewEnabled: row.ReviewEnabled, RetryPolicy: row.RetryPolicy, ID: projectID,
		}); err != nil {
			return fmt.Errorf("store: update settings: %w", err)
		}
		out, err = projectFromRow(row)
		return err
	})
	return out, err
}

func deleteProjectRecursive(ctx context.Context, e engine, projectID string) error {
	return e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		n, err := q.DeleteProject(ctx, projectID)
		if err != nil {
			return fmt.Errorf("store: delete project: %w", err)
		}
		if n == 0 {
			return domain.NotFound("Project", projectID)
		}
		// The one project-owned table without a foreign key to projects.
		if err := q.DeleteProjectIdempotencyKeys(ctx, projectID); err != nil {
			return fmt.Errorf("store: delete project: %w", err)
		}
		return nil
	})
}

// projectFromRow maps a projects row to a Project, the settings JSON split as the type says.
func projectFromRow(r sqlitedb.Project) (Project, error) {
	p := Project{ID: r.ID, Name: r.Name, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
	p.Settings.ReviewEnabled = r.ReviewEnabled == 1
	if err := json.Unmarshal([]byte(r.Settings), &p.Settings.Other); err != nil {
		return Project{}, fmt.Errorf("store: project %s settings: %w", r.ID, err)
	}
	if r.RetryPolicy.Valid {
		var rp domain.RetryPolicy
		if err := json.Unmarshal([]byte(r.RetryPolicy.String), &rp); err != nil {
			return Project{}, fmt.Errorf("store: project %s retry policy: %w", r.ID, err)
		}
		p.Settings.RetryPolicy = &rp
	}
	return p, nil
}

func (p pgQueries) CreateProject(ctx context.Context, arg sqlitedb.CreateProjectParams) error {
	return p.q.CreateProject(ctx, pgdb.CreateProjectParams(arg))
}

func (p pgQueries) GetProject(ctx context.Context, id string) (sqlitedb.Project, error) {
	r, err := p.q.GetProject(ctx, id)
	return sqlitedb.Project(r), err
}

func (p pgQueries) LockProject(ctx context.Context, id string) (sqlitedb.Project, error) {
	r, err := p.q.LockProject(ctx, id)
	return sqlitedb.Project(r), err
}

func (p pgQueries) ListProjectsForIdentity(ctx context.Context, identityID string) ([]sqlitedb.ListProjectsForIdentityRow, error) {
	rows, err := p.q.ListProjectsForIdentity(ctx, identityID)
	return convert(rows, func(r pgdb.ListProjectsForIdentityRow) sqlitedb.ListProjectsForIdentityRow {
		return sqlitedb.ListProjectsForIdentityRow(r)
	}), err
}

func (p pgQueries) UpdateProjectSettings(ctx context.Context, arg sqlitedb.UpdateProjectSettingsParams) error {
	return p.q.UpdateProjectSettings(ctx, pgdb.UpdateProjectSettingsParams(arg))
}

func (p pgQueries) DeleteProject(ctx context.Context, id string) (int64, error) {
	return p.q.DeleteProject(ctx, id)
}
