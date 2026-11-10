package memstore

import (
	"context"
	"maps"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The memory area of contract_memory.go. Its foreign keys are the engines' after migration 0003: an
// entry goes with its project, never with its task (DeleteMemoryByTask is the task cascade). A
// BlobSHA256 must name a blob of this store (blobs.go) or the write is NOT_FOUND.

func cloneMemory(e store.MemoryEntry) store.MemoryEntry {
	e.Value = cloneBytes(e.Value)
	return e
}

func isTaskScope(s store.MemoryScope) bool {
	return s == store.MemoryTask || s == store.MemoryAgent || s == store.MemoryShared
}

func liveEntry(e store.MemoryEntry, at int64) bool { return e.ExpiresAt == 0 || e.ExpiresAt > at }

// newestFirst sorts (ts, id) descending, ids as bytes.
func memoryNewestFirst(es []store.MemoryEntry) {
	slices.SortFunc(es, func(a, b store.MemoryEntry) int { return -byTSAndID(a.TS, b.TS, a.ID, b.ID) })
}

func (s *Store) WriteMemory(ctx context.Context, in store.WriteMemoryInput) (store.MemoryEntry, error) {
	if in.Key == "" || in.Author == "" {
		return store.MemoryEntry{}, domain.Validation("a memory entry needs a key and an author", nil)
	}
	e := store.MemoryEntry{ID: newID("M-", 16), Scope: in.Scope, Key: in.Key, Value: cloneBytes(in.Value), BlobSHA256: in.BlobSHA256, Author: in.Author, ExpiresAt: in.ExpiresAt}
	switch {
	case isTaskScope(in.Scope):
		if in.TaskID == "" {
			return store.MemoryEntry{}, domain.Validation("a "+string(in.Scope)+" memory entry needs a task", nil)
		}
		e.ProjectID, e.TaskID = in.ProjectID, in.TaskID
	case in.Scope == store.MemoryProject:
		if in.ProjectID == "" {
			return store.MemoryEntry{}, domain.Validation("a project memory entry needs a project", nil)
		}
		e.ProjectID, e.TaskID = in.ProjectID, in.TaskID
	case in.Scope == store.MemoryAgentGlobal:
	default:
		return store.MemoryEntry{}, domain.Validation("unknown memory scope: "+string(in.Scope), map[string]any{"scope": in.Scope})
	}
	return get(ctx, s, func(st *state) (store.MemoryEntry, error) {
		switch {
		case isTaskScope(in.Scope):
			if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
				return store.MemoryEntry{}, err
			}
		case in.Scope == store.MemoryProject:
			if _, ok := st.projects[in.ProjectID]; !ok {
				return store.MemoryEntry{}, domain.NotFound("Project", in.ProjectID)
			}
		}
		if _, ok := st.blobs[in.BlobSHA256]; in.BlobSHA256 != "" && !ok {
			return store.MemoryEntry{}, domain.NotFound("Blob", in.BlobSHA256)
		}
		e.TS = nowMillis()
		st.memory[e.ID] = cloneMemory(e)
		return e, nil
	})
}

func (s *Store) ReadMemory(ctx context.Context, projectID, taskID string, q store.ReadMemoryQuery) ([]store.MemoryEntry, error) {
	if q.Scope != "" && !isTaskScope(q.Scope) {
		return nil, domain.Validation("ReadMemory reads the task, agent and shared scopes; recall reads "+string(q.Scope), map[string]any{"scope": q.Scope})
	}
	return get(ctx, s, func(st *state) ([]store.MemoryEntry, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return nil, err
		}
		at := nowMillis()
		var window []store.MemoryEntry
		for _, e := range st.memory {
			if e.ProjectID == projectID && e.TaskID == taskID && isTaskScope(e.Scope) && liveEntry(e, at) &&
				(q.Scope == "" || e.Scope == q.Scope) && (q.Key == "" || e.Key == q.Key) &&
				(e.Scope != store.MemoryAgent || e.Author == q.Identity) {
				window = append(window, e)
			}
		}
		memoryNewestFirst(window)
		window = window[:min(len(window), clamp(q.Limit, 200, 200))]
		out := make([]store.MemoryEntry, len(window))
		for i, e := range window {
			out[i] = cloneMemory(e)
		}
		return out, nil
	})
}

func (s *Store) RecallMemory(ctx context.Context, q store.RecallMemoryQuery) ([]store.MemoryEntry, error) {
	if q.Scope != store.MemoryProject && q.Scope != store.MemoryAgentGlobal {
		return nil, domain.Validation("recall reads the project and agent_global scopes, not "+string(q.Scope), map[string]any{"scope": q.Scope})
	}
	if q.Limit < 1 || q.Limit > 1000 {
		return nil, domain.Validation("a recall limit is 1 to 1000", map[string]any{"limit": q.Limit})
	}
	return get(ctx, s, func(st *state) ([]store.MemoryEntry, error) {
		at := nowMillis()
		var rows []store.MemoryEntry
		for _, e := range st.memory {
			mine := (q.Scope == store.MemoryProject && e.Scope == store.MemoryProject && e.ProjectID == q.ProjectID) ||
				(q.Scope == store.MemoryAgentGlobal && e.Scope == store.MemoryAgentGlobal && e.Author == q.Identity)
			// With a key, the expired are left out before the limit; with none, after it.
			if mine && (q.Key == "" || (e.Key == q.Key && liveEntry(e, at))) {
				rows = append(rows, e)
			}
		}
		memoryNewestFirst(rows)
		rows = rows[:min(len(rows), q.Limit)]
		out := []store.MemoryEntry{}
		for _, e := range rows {
			if liveEntry(e, at) {
				out = append(out, cloneMemory(e))
			}
		}
		return out, nil
	})
}

func (s *Store) DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		var expired []store.MemoryEntry
		for _, e := range st.memory {
			if e.ExpiresAt != 0 && e.ExpiresAt <= now {
				expired = append(expired, e)
			}
		}
		// Oldest expiry first, as the engines' sweep takes them.
		slices.SortFunc(expired, func(a, b store.MemoryEntry) int { return byTSAndID(a.ExpiresAt, b.ExpiresAt, a.ID, b.ID) })
		expired = expired[:min(len(expired), limit)]
		for _, e := range expired {
			delete(st.memory, e.ID)
		}
		return int64(len(expired)), nil
	})
}

func (s *Store) DeleteMemoryByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		before := len(st.memory)
		maps.DeleteFunc(st.memory, func(_ string, e store.MemoryEntry) bool {
			return e.ProjectID == projectID && e.TaskID == taskID && isTaskScope(e.Scope)
		})
		return int64(before - len(st.memory)), nil
	})
}
