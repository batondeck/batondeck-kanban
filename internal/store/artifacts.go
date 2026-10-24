package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The artifact area of contract_artifacts.go, on both engines. Each call is one statement; the
// insert and the list start FROM the task, so a missing task is their own empty answer.

type artifactQueries interface {
	InsertArtifact(ctx context.Context, arg sqlitedb.InsertArtifactParams) (sqlitedb.Artifact, error)
	ListArtifacts(ctx context.Context, arg sqlitedb.ListArtifactsParams) ([]sqlitedb.ListArtifactsRow, error)
	RetractArtifact(ctx context.Context, arg sqlitedb.RetractArtifactParams) (sqlitedb.Artifact, error)
	ListLiveArtifactTaskIDs(ctx context.Context, arg sqlitedb.ListLiveArtifactTaskIDsParams) ([]string, error)
}

func (s *SQLite) AddArtifact(ctx context.Context, in AddArtifactInput) (Artifact, error) {
	return addArtifact(ctx, s, in)
}

func (s *Postgres) AddArtifact(ctx context.Context, in AddArtifactInput) (Artifact, error) {
	return addArtifact(ctx, s, in)
}

func (s *SQLite) ListArtifacts(ctx context.Context, projectID, taskID string) ([]Artifact, error) {
	return listArtifacts(ctx, s, projectID, taskID)
}

func (s *Postgres) ListArtifacts(ctx context.Context, projectID, taskID string) ([]Artifact, error) {
	return listArtifacts(ctx, s, projectID, taskID)
}

func (s *SQLite) RetractArtifact(ctx context.Context, projectID, taskID, id string, r ArtifactRetraction) (Artifact, error) {
	return retractArtifact(ctx, s, projectID, taskID, id, r)
}

func (s *Postgres) RetractArtifact(ctx context.Context, projectID, taskID, id string, r ArtifactRetraction) (Artifact, error) {
	return retractArtifact(ctx, s, projectID, taskID, id, r)
}

func (s *SQLite) LiveArtifactTasks(ctx context.Context, projectID string, taskIDs []string) (map[string]bool, error) {
	return liveArtifactTasks(ctx, s, projectID, taskIDs)
}

func (s *Postgres) LiveArtifactTasks(ctx context.Context, projectID string, taskIDs []string) (map[string]bool, error) {
	return liveArtifactTasks(ctx, s, projectID, taskIDs)
}

// ArtifactKinds is every artifact kind, as the schema's CHECK lists them.
var ArtifactKinds = map[ArtifactKind]bool{ArtifactPR: true, ArtifactBranch: true, ArtifactCommit: true, ArtifactFile: true, ArtifactDoc: true, ArtifactAttachment: true}

// CheckArtifactInput is the area's validation, shared by memstore so both refuse alike.
func CheckArtifactInput(in AddArtifactInput) error {
	empty := func(p *string) bool { return p == nil || *p == "" }
	switch {
	case !ArtifactKinds[in.Kind]:
		return domain.Validation("unknown artifact kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case empty(in.URL) && empty(in.Ref):
		return domain.Validation("an artifact needs a url or a ref", nil)
	case in.AddedBy == "":
		return domain.Validation("an artifact needs an addedBy", nil)
	}
	return nil
}

func addArtifact(ctx context.Context, e engine, in AddArtifactInput) (Artifact, error) {
	if err := CheckArtifactInput(in); err != nil {
		return Artifact{}, err
	}
	row, err := e.queries(true).InsertArtifact(ctx, sqlitedb.InsertArtifactParams{
		ID: newID("AR-", 16), Kind: string(in.Kind), Url: nullStr(in.URL), Ref: nullStr(in.Ref), Repo: nullStr(in.Repo),
		Title: nullStr(in.Title), AddedBy: in.AddedBy, AddedByAgent: nullStr(in.AddedByAgent), Ts: nowMillis(),
		ProjectID: in.ProjectID, TaskID: in.TaskID,
	})
	if err := found(err, "Task", in.TaskID); err != nil {
		return Artifact{}, err
	}
	return artifactOf(row), nil
}

func artifactOf(r sqlitedb.Artifact) Artifact {
	a := Artifact{ProjectID: r.ProjectID, TaskID: r.TaskID, ID: r.ID, Kind: ArtifactKind(r.Kind), URL: strPtr(r.Url),
		Ref: strPtr(r.Ref), Repo: strPtr(r.Repo), Title: strPtr(r.Title), AddedBy: r.AddedBy, AddedByAgent: strPtr(r.AddedByAgent), TS: r.Ts}
	if r.RetractedAt.Valid {
		a.Retraction = &ArtifactRetraction{By: r.RetractedBy.String, ByAgent: strPtr(r.RetractedByAgent), At: r.RetractedAt.Int64, Reason: r.RetractedReason.String}
	}
	return a
}

func listArtifacts(ctx context.Context, e engine, projectID, taskID string) ([]Artifact, error) {
	rows, err := e.queries(false).ListArtifacts(ctx, sqlitedb.ListArtifactsParams{ProjectID: projectID, TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("store: list artifacts: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", taskID)
	}
	out := []Artifact{}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		out = append(out, artifactOf(sqlitedb.Artifact{ProjectID: projectID, TaskID: taskID, ID: r.ID.String, Kind: r.Kind.String,
			Url: r.Url, Ref: r.Ref, Repo: r.Repo, Title: r.Title, AddedBy: r.AddedBy.String, AddedByAgent: r.AddedByAgent, Ts: r.Ts.Int64,
			RetractedBy: r.RetractedBy, RetractedByAgent: r.RetractedByAgent, RetractedAt: r.RetractedAt, RetractedReason: r.RetractedReason}))
	}
	return out, nil
}

func retractArtifact(ctx context.Context, e engine, projectID, taskID, id string, r ArtifactRetraction) (Artifact, error) {
	if r.By == "" || r.Reason == "" {
		return Artifact{}, domain.Validation("a retraction needs a by and a reason", nil)
	}
	row, err := e.queries(true).RetractArtifact(ctx, sqlitedb.RetractArtifactParams{
		RetractedBy: r.By, RetractedByAgent: nullStr(r.ByAgent), RetractedAt: r.At, RetractedReason: r.Reason,
		ProjectID: projectID, TaskID: taskID, ID: id,
	})
	if err := found(err, "Artifact", id); err != nil {
		return Artifact{}, err
	}
	return artifactOf(row), nil
}

func liveArtifactTasks(ctx context.Context, e engine, projectID string, taskIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	ids, err := e.queries(false).ListLiveArtifactTaskIDs(ctx, sqlitedb.ListLiveArtifactTaskIDsParams{ProjectID: projectID, TaskIds: taskIDs})
	if err != nil {
		return nil, fmt.Errorf("store: live artifact tasks: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertArtifact(ctx context.Context, arg sqlitedb.InsertArtifactParams) (sqlitedb.Artifact, error) {
	r, err := p.q.InsertArtifact(ctx, pgdb.InsertArtifactParams(arg))
	return sqlitedb.Artifact(r), err
}

func (p pgQueries) ListArtifacts(ctx context.Context, arg sqlitedb.ListArtifactsParams) ([]sqlitedb.ListArtifactsRow, error) {
	rows, err := p.q.ListArtifacts(ctx, pgdb.ListArtifactsParams(arg))
	return convert(rows, func(r pgdb.ListArtifactsRow) sqlitedb.ListArtifactsRow { return sqlitedb.ListArtifactsRow(r) }), err
}

func (p pgQueries) RetractArtifact(ctx context.Context, arg sqlitedb.RetractArtifactParams) (sqlitedb.Artifact, error) {
	r, err := p.q.RetractArtifact(ctx, pgdb.RetractArtifactParams(arg))
	return sqlitedb.Artifact(r), err
}

func (p pgQueries) ListLiveArtifactTaskIDs(ctx context.Context, arg sqlitedb.ListLiveArtifactTaskIDsParams) ([]string, error) {
	return p.q.ListLiveArtifactTaskIDs(ctx, pgdb.ListLiveArtifactTaskIDsParams(arg))
}
