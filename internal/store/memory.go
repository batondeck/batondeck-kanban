package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The memory area of contract_memory.go, on both engines. ReadMemory and RecallMemory are one
// statement each, with the TTL judged in the statement at the read's own time. A write checks the
// task (or project) and the blob it names, then inserts, inside one transaction.

type memoryQueries interface {
	InsertMemory(ctx context.Context, arg sqlitedb.InsertMemoryParams) (sqlitedb.Memory, error)
	BlobExists(ctx context.Context, sha256 string) (int64, error)
	ReadMemoryWindow(ctx context.Context, arg sqlitedb.ReadMemoryWindowParams) ([]sqlitedb.ReadMemoryWindowRow, error)
	RecallMemory(ctx context.Context, arg sqlitedb.RecallMemoryParams) ([]sqlitedb.Memory, error)
	DeleteExpiredMemory(ctx context.Context, arg sqlitedb.DeleteExpiredMemoryParams) (int64, error)
	DeleteMemoryByTask(ctx context.Context, arg sqlitedb.DeleteMemoryByTaskParams) (int64, error)
}

func (s *SQLite) WriteMemory(ctx context.Context, in WriteMemoryInput) (MemoryEntry, error) {
	return writeMemory(ctx, s, in)
}

func (s *Postgres) WriteMemory(ctx context.Context, in WriteMemoryInput) (MemoryEntry, error) {
	return writeMemory(ctx, s, in)
}

func (s *SQLite) ReadMemory(ctx context.Context, projectID, taskID string, q ReadMemoryQuery) ([]MemoryEntry, error) {
	return readMemory(ctx, s, projectID, taskID, q)
}

func (s *Postgres) ReadMemory(ctx context.Context, projectID, taskID string, q ReadMemoryQuery) ([]MemoryEntry, error) {
	return readMemory(ctx, s, projectID, taskID, q)
}

func (s *SQLite) RecallMemory(ctx context.Context, q RecallMemoryQuery) ([]MemoryEntry, error) {
	return recallMemory(ctx, s, q)
}

func (s *Postgres) RecallMemory(ctx context.Context, q RecallMemoryQuery) ([]MemoryEntry, error) {
	return recallMemory(ctx, s, q)
}

func (s *SQLite) DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpiredMemory(ctx, s, now, limit)
}

func (s *Postgres) DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpiredMemory(ctx, s, now, limit)
}

func (s *SQLite) DeleteMemoryByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteMemoryByTask(ctx, s, projectID, taskID)
}

func (s *Postgres) DeleteMemoryByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteMemoryByTask(ctx, s, projectID, taskID)
}

// isTaskScope reports the three scopes that live under a task.
func isTaskScope(s MemoryScope) bool {
	return s == MemoryTask || s == MemoryAgent || s == MemoryShared
}

func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullMillis(n int64) sql.NullInt64 {
	return sql.NullInt64{Int64: n, Valid: n != 0}
}

func writeMemory(ctx context.Context, e engine, in WriteMemoryInput) (MemoryEntry, error) {
	if in.Key == "" || in.Author == "" {
		return MemoryEntry{}, domain.Validation("a memory entry needs a key and an author", nil)
	}
	p := sqlitedb.InsertMemoryParams{
		ID: newID("M-", 16), Scope: string(in.Scope), Key: in.Key, Value: nullRaw(in.Value),
		BlobSha256: nullText(in.BlobSHA256), Author: in.Author, Ts: nowMillis(), ExpiresAt: nullMillis(in.ExpiresAt),
	}
	switch {
	case isTaskScope(in.Scope):
		if in.TaskID == "" {
			return MemoryEntry{}, domain.Validation("a "+string(in.Scope)+" memory entry needs a task", nil)
		}
		p.ProjectID, p.TaskID = nullText(in.ProjectID), nullText(in.TaskID)
	case in.Scope == MemoryProject:
		if in.ProjectID == "" {
			return MemoryEntry{}, domain.Validation("a project memory entry needs a project", nil)
		}
		p.ProjectID, p.TaskID = nullText(in.ProjectID), nullText(in.TaskID)
	case in.Scope == MemoryAgentGlobal:
	default:
		return MemoryEntry{}, domain.Validation("unknown memory scope: "+string(in.Scope), map[string]any{"scope": in.Scope})
	}
	var out MemoryEntry
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		switch {
		case isTaskScope(in.Scope):
			if _, err := q.GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: in.ProjectID, ID: in.TaskID}); err != nil {
				return found(err, "Task", in.TaskID)
			}
		case in.Scope == MemoryProject:
			if _, err := q.GetProject(ctx, in.ProjectID); err != nil {
				return found(err, "Project", in.ProjectID)
			}
		}
		if in.BlobSHA256 != "" {
			n, err := q.BlobExists(ctx, in.BlobSHA256)
			if err != nil {
				return fmt.Errorf("store: blob: %w", err)
			}
			if n == 0 {
				return domain.NotFound("Blob", in.BlobSHA256)
			}
		}
		r, err := q.InsertMemory(ctx, p)
		if err != nil {
			return fmt.Errorf("store: write memory: %w", err)
		}
		out = memoryFrom(r)
		return nil
	})
	return out, err
}

func readMemory(ctx context.Context, e engine, projectID, taskID string, q ReadMemoryQuery) ([]MemoryEntry, error) {
	if q.Scope != "" && !isTaskScope(q.Scope) {
		return nil, domain.Validation("ReadMemory reads the task, agent and shared scopes; recall reads "+string(q.Scope), map[string]any{"scope": q.Scope})
	}
	rows, err := e.queries(false).ReadMemoryWindow(ctx, sqlitedb.ReadMemoryWindowParams{
		WindowLimit: int64(clamp(q.Limit, 200, 200)), Now: nowMillis(), Scope: string(q.Scope), Key: q.Key,
		Identity: q.Identity, ProjectID: projectID, TaskID: taskID,
	})
	if err != nil {
		return nil, fmt.Errorf("store: read memory: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", taskID)
	}
	out := []MemoryEntry{}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		out = append(out, MemoryEntry{
			ID: r.ID.String, ProjectID: projectID, TaskID: taskID, Scope: MemoryScope(r.Scope.String), Key: r.Key.String,
			Value: rawOrNil(r.Value), BlobSHA256: r.BlobSha256.String, Author: r.Author.String, TS: r.Ts.Int64,
			ExpiresAt: r.ExpiresAt.Int64,
		})
	}
	return out, nil
}

func recallMemory(ctx context.Context, e engine, q RecallMemoryQuery) ([]MemoryEntry, error) {
	if q.Scope != MemoryProject && q.Scope != MemoryAgentGlobal {
		return nil, domain.Validation("recall reads the project and agent_global scopes, not "+string(q.Scope), map[string]any{"scope": q.Scope})
	}
	if q.Limit < 1 || q.Limit > 1000 {
		return nil, domain.Validation("a recall limit is 1 to 1000", map[string]any{"limit": q.Limit})
	}
	rows, err := e.queries(false).RecallMemory(ctx, sqlitedb.RecallMemoryParams{
		Scope: string(q.Scope), ProjectID: q.ProjectID, Identity: q.Identity, Key: q.Key, Now: nowMillis(), PageLimit: int64(q.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("store: recall memory: %w", err)
	}
	out := make([]MemoryEntry, len(rows))
	for i, r := range rows {
		out[i] = memoryFrom(r)
	}
	return out, nil
}

func deleteExpiredMemory(ctx context.Context, e engine, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	n, err := e.queries(true).DeleteExpiredMemory(ctx, sqlitedb.DeleteExpiredMemoryParams{Now: now, PageLimit: int64(limit)})
	if err != nil {
		return 0, fmt.Errorf("store: sweep memory: %w", err)
	}
	return n, nil
}

func deleteMemoryByTask(ctx context.Context, e engine, projectID, taskID string) (int64, error) {
	n, err := e.queries(true).DeleteMemoryByTask(ctx, sqlitedb.DeleteMemoryByTaskParams{ProjectID: nullText(projectID), TaskID: nullText(taskID)})
	if err != nil {
		return 0, fmt.Errorf("store: delete memory: %w", err)
	}
	return n, nil
}

func memoryFrom(r sqlitedb.Memory) MemoryEntry {
	var value json.RawMessage
	if r.Value.Valid {
		value = json.RawMessage(r.Value.String)
	}
	return MemoryEntry{
		ID: r.ID, ProjectID: r.ProjectID.String, TaskID: r.TaskID.String, Scope: MemoryScope(r.Scope), Key: r.Key,
		Value: value, BlobSHA256: r.BlobSha256.String, Author: r.Author, TS: r.Ts, ExpiresAt: r.ExpiresAt.Int64,
	}
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertMemory(ctx context.Context, arg sqlitedb.InsertMemoryParams) (sqlitedb.Memory, error) {
	r, err := p.q.InsertMemory(ctx, pgdb.InsertMemoryParams(arg))
	return sqlitedb.Memory(r), err
}

func (p pgQueries) BlobExists(ctx context.Context, sha256 string) (int64, error) {
	return p.q.BlobExists(ctx, sha256)
}

func (p pgQueries) ReadMemoryWindow(ctx context.Context, arg sqlitedb.ReadMemoryWindowParams) ([]sqlitedb.ReadMemoryWindowRow, error) {
	rows, err := p.q.ReadMemoryWindow(ctx, pgdb.ReadMemoryWindowParams(arg))
	return convert(rows, func(r pgdb.ReadMemoryWindowRow) sqlitedb.ReadMemoryWindowRow { return sqlitedb.ReadMemoryWindowRow(r) }), err
}

func (p pgQueries) RecallMemory(ctx context.Context, arg sqlitedb.RecallMemoryParams) ([]sqlitedb.Memory, error) {
	rows, err := p.q.RecallMemory(ctx, pgdb.RecallMemoryParams(arg))
	return convert(rows, func(r pgdb.Memory) sqlitedb.Memory { return sqlitedb.Memory(r) }), err
}

func (p pgQueries) DeleteExpiredMemory(ctx context.Context, arg sqlitedb.DeleteExpiredMemoryParams) (int64, error) {
	return p.q.DeleteExpiredMemory(ctx, pgdb.DeleteExpiredMemoryParams(arg))
}

func (p pgQueries) DeleteMemoryByTask(ctx context.Context, arg sqlitedb.DeleteMemoryByTaskParams) (int64, error) {
	return p.q.DeleteMemoryByTask(ctx, pgdb.DeleteMemoryByTaskParams(arg))
}
