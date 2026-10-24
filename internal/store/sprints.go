package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The sprint area of contract_sprints.go, on both engines. A create is one INSERT, its missing board
// the foreign key's refusal; an update is the oncall shape, the row read to merge then one
// compare-and-set UPDATE … RETURNING; the member count is one UPDATE; a page and the members are one
// SELECT each.

type sprintQueries interface {
	InsertSprint(ctx context.Context, arg sqlitedb.InsertSprintParams) (sqlitedb.Sprint, error)
	GetSprint(ctx context.Context, arg sqlitedb.GetSprintParams) (sqlitedb.Sprint, error)
	ListSprints(ctx context.Context, arg sqlitedb.ListSprintsParams) ([]sqlitedb.Sprint, error)
	UpdateSprint(ctx context.Context, arg sqlitedb.UpdateSprintParams) (sqlitedb.Sprint, error)
	AddSprintMembers(ctx context.Context, arg sqlitedb.AddSprintMembersParams) (sqlitedb.Sprint, error)
	ListSprintMembers(ctx context.Context, arg sqlitedb.ListSprintMembersParams) ([]sqlitedb.ListSprintMembersRow, error)
}

func (s *SQLite) CreateSprint(ctx context.Context, in CreateSprintInput) (Sprint, error) {
	return createSprint(ctx, s, in)
}

func (s *Postgres) CreateSprint(ctx context.Context, in CreateSprintInput) (Sprint, error) {
	return createSprint(ctx, s, in)
}

func (s *SQLite) GetSprint(ctx context.Context, projectID, sprintID string) (Sprint, error) {
	return getSprint(ctx, s, projectID, sprintID)
}

func (s *Postgres) GetSprint(ctx context.Context, projectID, sprintID string) (Sprint, error) {
	return getSprint(ctx, s, projectID, sprintID)
}

func (s *SQLite) ListSprints(ctx context.Context, projectID string, f SprintFilter, cursor string, limit int) (SprintPage, error) {
	return listSprints(ctx, s, projectID, f, cursor, limit)
}

func (s *Postgres) ListSprints(ctx context.Context, projectID string, f SprintFilter, cursor string, limit int) (SprintPage, error) {
	return listSprints(ctx, s, projectID, f, cursor, limit)
}

func (s *SQLite) UpdateSprint(ctx context.Context, projectID, sprintID string, expectedVersion int64, p SprintPatch) (Sprint, error) {
	return updateSprint(ctx, s, projectID, sprintID, expectedVersion, p)
}

func (s *Postgres) UpdateSprint(ctx context.Context, projectID, sprintID string, expectedVersion int64, p SprintPatch) (Sprint, error) {
	return updateSprint(ctx, s, projectID, sprintID, expectedVersion, p)
}

func (s *SQLite) AddSprintMembers(ctx context.Context, projectID, sprintID string, delta int64) (Sprint, error) {
	return addSprintMembers(ctx, s, projectID, sprintID, delta)
}

func (s *Postgres) AddSprintMembers(ctx context.Context, projectID, sprintID string, delta int64) (Sprint, error) {
	return addSprintMembers(ctx, s, projectID, sprintID, delta)
}

func (s *SQLite) ListSprintMembers(ctx context.Context, projectID, sprintID string, limit int) ([]Task, error) {
	return listSprintMembers(ctx, s, projectID, sprintID, limit)
}

func (s *Postgres) ListSprintMembers(ctx context.Context, projectID, sprintID string, limit int) ([]Task, error) {
	return listSprintMembers(ctx, s, projectID, sprintID, limit)
}

// NewSprintID is a sprint id: "S-" and 12 random hex digits, conductor's shape (never counter-minted).
func NewSprintID() string { return newID("S-", 12) }

// CheckCreateSprint and CheckSprintPatch are the shapes a sprint write must have, shared with
// memstore.
func CheckCreateSprint(in CreateSprintInput) error {
	if in.Name == "" || in.Goal == "" || in.Acceptance == "" || in.CreatedBy == "" || in.CreatedAt <= 0 {
		return domain.Validation("a sprint needs a name, a goal, acceptance, a creator and a time", nil)
	}
	return nil
}

func CheckSprintPatch(p SprintPatch) error {
	if err := validWriter(p.UpdatedBy, p.UpdatedAt); err != nil {
		return err
	}
	if p.Name != nil && *p.Name == "" {
		return domain.Validation("a sprint's name is not empty", map[string]any{"field": "name"})
	}
	if p.Status != nil {
		if _, err := domain.ParseSprintStatus(string(*p.Status)); err != nil {
			return err
		}
	}
	return nil
}

// ApplySprintPatch is cur with p's fields written, the merge both the engines and memstore make.
func ApplySprintPatch(cur Sprint, p SprintPatch) Sprint {
	next := cur
	for _, f := range []struct {
		dst *string
		src *string
	}{{&next.Name, p.Name}, {&next.Goal, p.Goal}, {&next.Acceptance, p.Acceptance}} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if p.Status != nil {
		next.Status = *p.Status
	}
	setNullable(&next.ProposalTaskID, p.ProposalTaskID)
	setNullable(&next.ApprovedBy, p.ApprovedBy)
	setNullable(&next.Checkpoint, p.Checkpoint)
	setNullable(&next.EventCursor, p.EventCursor)
	setNullable(&next.StartedAt, p.StartedAt)
	setNullable(&next.ClosedAt, p.ClosedAt)
	next.UpdatedBy, next.UpdatedAt = p.UpdatedBy, p.UpdatedAt
	return next
}

// SprintCursor reads a ListSprints cursor: "" is the first page (after id "").
func SprintCursor(cursor string) (createdAt int64, id string, err error) {
	if cursor == "" {
		return 0, "", nil
	}
	order, id, err := DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	createdAt, perr := strconv.ParseInt(order, 10, 64)
	if perr != nil || id == "" {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return createdAt, id, nil
}

func createSprint(ctx context.Context, e engine, in CreateSprintInput) (Sprint, error) {
	if err := CheckCreateSprint(in); err != nil {
		return Sprint{}, err
	}
	r, err := e.queries(true).InsertSprint(ctx, sqlitedb.InsertSprintParams{
		ProjectID: in.ProjectID, ID: NewSprintID(), BoardID: in.BoardID, Name: in.Name, Goal: in.Goal,
		Acceptance: in.Acceptance, CreatedBy: in.CreatedBy, CreatedAt: in.CreatedAt, UpdatedBy: in.CreatedBy, UpdatedAt: in.CreatedAt,
	})
	if isForeignKeyViolation(err) {
		return Sprint{}, domain.NotFound("Board", in.BoardID)
	}
	if err != nil {
		return Sprint{}, fmt.Errorf("store: create sprint: %w", err)
	}
	return sprintFrom(r), nil
}

func getSprint(ctx context.Context, e engine, projectID, sprintID string) (Sprint, error) {
	r, err := e.queries(false).GetSprint(ctx, sqlitedb.GetSprintParams{ProjectID: projectID, ID: sprintID})
	if err := found(err, "Sprint", sprintID); err != nil {
		return Sprint{}, err
	}
	return sprintFrom(r), nil
}

func listSprints(ctx context.Context, e engine, projectID string, f SprintFilter, cursor string, limit int) (SprintPage, error) {
	afterCreated, afterID, err := SprintCursor(cursor)
	if err != nil {
		return SprintPage{}, err
	}
	status := ""
	if f.Status != nil {
		if _, err := domain.ParseSprintStatus(string(*f.Status)); err != nil {
			return SprintPage{}, err
		}
		status = string(*f.Status)
	}
	limit = clamp(limit, 50, 200)
	rows, err := e.queries(false).ListSprints(ctx, sqlitedb.ListSprintsParams{
		ProjectID: projectID, BoardID: f.BoardID, Status: status, AfterID: afterID, AfterCreated: afterCreated, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return SprintPage{}, fmt.Errorf("store: list sprints: %w", err)
	}
	page := SprintPage{Sprints: []Sprint{}}
	for i, r := range rows {
		if i == limit {
			last := page.Sprints[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.CreatedAt, 10), last.ID)
			break
		}
		page.Sprints = append(page.Sprints, sprintFrom(r))
	}
	return page, nil
}

func updateSprint(ctx context.Context, e engine, projectID, sprintID string, expectedVersion int64, p SprintPatch) (Sprint, error) {
	if err := CheckSprintPatch(p); err != nil {
		return Sprint{}, err
	}
	cur, err := getSprint(ctx, e, projectID, sprintID)
	if err != nil {
		return Sprint{}, err
	}
	if cur.Version != expectedVersion {
		return Sprint{}, domain.Stale(expectedVersion, cur.Version)
	}
	next := ApplySprintPatch(cur, p)
	r, err := e.queries(true).UpdateSprint(ctx, sqlitedb.UpdateSprintParams{
		Name: next.Name, Goal: next.Goal, Acceptance: next.Acceptance, Status: string(next.Status),
		ProposalTaskID: nullStr(next.ProposalTaskID), ApprovedBy: nullStr(next.ApprovedBy), Checkpoint: nullStr(next.Checkpoint),
		EventCursor: nullStr(next.EventCursor), StartedAt: nullInt(next.StartedAt), ClosedAt: nullInt(next.ClosedAt),
		UpdatedBy: next.UpdatedBy, UpdatedAt: next.UpdatedAt, ProjectID: projectID, ID: sprintID, Version: expectedVersion,
	})
	if isNoRows(err) {
		// Another writer moved it between the read and the write: say which way.
		now, gerr := getSprint(ctx, e, projectID, sprintID)
		if gerr != nil {
			return Sprint{}, gerr
		}
		return Sprint{}, domain.Stale(expectedVersion, now.Version)
	}
	if err != nil {
		return Sprint{}, fmt.Errorf("store: update sprint: %w", err)
	}
	return sprintFrom(r), nil
}

func addSprintMembers(ctx context.Context, e engine, projectID, sprintID string, delta int64) (Sprint, error) {
	r, err := e.queries(true).AddSprintMembers(ctx, sqlitedb.AddSprintMembersParams{Delta: delta, ProjectID: projectID, ID: sprintID})
	if err := found(err, "Sprint", sprintID); err != nil {
		return Sprint{}, err
	}
	return sprintFrom(r), nil
}

func listSprintMembers(ctx context.Context, e engine, projectID, sprintID string, limit int) ([]Task, error) {
	rows, err := e.queries(false).ListSprintMembers(ctx, sqlitedb.ListSprintMembersParams{
		ProjectID: projectID, SprintID: sprintID, PageLimit: int64(clamp(limit, 500, 500)),
	})
	if err != nil {
		return nil, fmt.Errorf("store: list sprint members: %w", err)
	}
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		t, err := taskFromRow(sqlitedb.GetTaskRow(r))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func sprintFrom(r sqlitedb.Sprint) Sprint {
	return Sprint{
		ProjectID: r.ProjectID, ID: r.ID, BoardID: r.BoardID, Name: r.Name, Goal: r.Goal, Acceptance: r.Acceptance,
		Status: domain.SprintStatus(r.Status), ProposalTaskID: strPtr(r.ProposalTaskID), ApprovedBy: strPtr(r.ApprovedBy),
		MemberCount: r.MemberCount, Checkpoint: strPtr(r.Checkpoint), EventCursor: strPtr(r.EventCursor), Version: r.Version,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, StartedAt: intPtr(r.StartedAt), ClosedAt: intPtr(r.ClosedAt),
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt,
	}
}

func (p pgQueries) InsertSprint(ctx context.Context, arg sqlitedb.InsertSprintParams) (sqlitedb.Sprint, error) {
	r, err := p.q.InsertSprint(ctx, pgdb.InsertSprintParams(arg))
	return sqlitedb.Sprint(r), err
}

func (p pgQueries) GetSprint(ctx context.Context, arg sqlitedb.GetSprintParams) (sqlitedb.Sprint, error) {
	r, err := p.q.GetSprint(ctx, pgdb.GetSprintParams(arg))
	return sqlitedb.Sprint(r), err
}

func (p pgQueries) ListSprints(ctx context.Context, arg sqlitedb.ListSprintsParams) ([]sqlitedb.Sprint, error) {
	rows, err := p.q.ListSprints(ctx, pgdb.ListSprintsParams(arg))
	return convert(rows, func(r pgdb.Sprint) sqlitedb.Sprint { return sqlitedb.Sprint(r) }), err
}

func (p pgQueries) UpdateSprint(ctx context.Context, arg sqlitedb.UpdateSprintParams) (sqlitedb.Sprint, error) {
	r, err := p.q.UpdateSprint(ctx, pgdb.UpdateSprintParams(arg))
	return sqlitedb.Sprint(r), err
}

func (p pgQueries) AddSprintMembers(ctx context.Context, arg sqlitedb.AddSprintMembersParams) (sqlitedb.Sprint, error) {
	r, err := p.q.AddSprintMembers(ctx, pgdb.AddSprintMembersParams(arg))
	return sqlitedb.Sprint(r), err
}

func (p pgQueries) ListSprintMembers(ctx context.Context, arg sqlitedb.ListSprintMembersParams) ([]sqlitedb.ListSprintMembersRow, error) {
	rows, err := p.q.ListSprintMembers(ctx, pgdb.ListSprintMembersParams(arg))
	return convert(rows, func(r pgdb.ListSprintMembersRow) sqlitedb.ListSprintMembersRow {
		return sqlitedb.ListSprintMembersRow(r)
	}), err
}
