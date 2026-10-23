package service

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	// memoryInlineMax is conductor's MEMORY_INLINE_MAX default (config/index.ts:540): the longest
	// serialised inline value.
	memoryInlineMax = 32_768
	// recallDefaultK and recallMaxK are recall_memory's k (default 20, max 100).
	recallDefaultK = 20
	recallMaxK     = 100
)

func taskScope(s domain.MemoryScope) bool {
	return s == domain.MemoryScopeTask || s == domain.MemoryScopeAgent || s == domain.MemoryScopeShared
}

// parseTTL is write_memory's ttl: conductor's MemoryEntry.ttl is the expiry instant as an ISO 8601
// string (util/time.ts isExpired), not a duration. "" never expires.
// DEVIATION: conductor stores a ttl JavaScript's Date cannot parse and never expires it; here it is
// VALIDATION.
func parseTTL(ttl string) (int64, error) {
	if ttl == "" {
		return 0, nil
	}
	at, err := time.Parse(time.RFC3339Nano, ttl)
	if err != nil {
		return 0, domain.Validation("ttl must be an ISO 8601 date-time (the entry's expiry)", map[string]any{"field": "ttl"})
	}
	return max(at.UnixMilli(), 1), nil
}

// WriteMemory is write_memory. A task scope (task, agent, shared) lives under taskId and bumps that
// task's version once; a durable scope (project, agent_global) moves no task, its taskId only
// provenance, checked to exist. Event memory.written {scope, key} on the task when one is named.
// DEVIATION: a durable entry with no taskId appends no event (the store has no project-level event,
// P-98); conductor appends one with no task.
func (s contextSvc) WriteMemory(ctx context.Context, caller domain.Caller, req WriteMemoryReq) (WriteMemoryResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "write_memory", domain.GrantSubject{MemoryScope: req.Scope}, req.TaskID != "")
	if err != nil {
		return WriteMemoryResp{}, err
	}
	if !slices.Contains([]domain.MemoryScope{domain.MemoryScopeTask, domain.MemoryScopeAgent, domain.MemoryScopeShared, domain.MemoryScopeProject, domain.MemoryScopeAgentGlobal}, req.Scope) {
		return WriteMemoryResp{}, domain.Validation("unknown memory scope: "+string(req.Scope), map[string]any{"field": "scope"})
	}
	task := taskScope(req.Scope)
	if task && req.TaskID == "" {
		return WriteMemoryResp{}, domain.Validation("taskId is required for memory scope '"+string(req.Scope)+"'", map[string]any{"field": "taskId"})
	}
	if err := required("key", req.Key); err != nil {
		return WriteMemoryResp{}, err
	}
	if err := maxChars("key", req.Key, maxKeyChars); err != nil {
		return WriteMemoryResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return WriteMemoryResp{}, err
	}
	if req.LargeArtifact != (LargeArtifact{}) {
		// TODO(spec): the signed-upload path waits for the blob store (R0.19 left Blobs unbuilt on
		// the engines); until it lands a large value is refused rather than written half.
		return WriteMemoryResp{}, domain.Validation("largeArtifact uploads are not available yet; write an inline value", map[string]any{"field": "largeArtifact"})
	}
	expires, err := parseTTL(req.TTL)
	if err != nil {
		return WriteMemoryResp{}, err
	}
	value := json.RawMessage("null")
	if req.Value != nil {
		// JSON.stringify's length: no HTML escaping, no trailing newline.
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(req.Value); err != nil {
			return WriteMemoryResp{}, domain.Validation("value is not JSON", map[string]any{"field": "value"})
		}
		value = bytes.TrimSuffix(b.Bytes(), []byte("\n"))
		if jsLen(string(value)) > memoryInlineMax {
			return WriteMemoryResp{}, domain.Validation("Memory value exceeds MEMORY_INLINE_MAX (32768); use largeArtifact", map[string]any{"field": "value"})
		}
	}
	m := mutation{
		Tool: "write_memory", Args: map[string]any{"projectId": req.ProjectID, "scope": req.Scope, "key": req.Key},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}
	// A durable entry's provenance task is loaded in fn, not by mutate (it is not bumped), so a grant
	// check pending on it is finished there.
	pend := a
	if task {
		m.TaskID = req.TaskID
	} else {
		m.Access.pending = false
	}
	return mutate(ctx, s.c, caller, m, func(t *txn) (WriteMemoryResp, error) {
		var on *store.Task
		switch {
		case t.Task != nil:
			on = t.Task
		case req.TaskID != "":
			prov, err := t.Tx.GetTask(ctx, req.ProjectID, req.TaskID)
			if err != nil {
				return WriteMemoryResp{}, err
			}
			if err := pend.permit(&prov); err != nil {
				return WriteMemoryResp{}, err
			}
			on = &prov
		}
		in := store.WriteMemoryInput{ProjectID: req.ProjectID, Scope: store.MemoryScope(req.Scope), Key: req.Key, Value: value, Author: caller.Identity, ExpiresAt: expires}
		if req.Scope != domain.MemoryScopeAgentGlobal {
			in.TaskID = req.TaskID
		}
		e, err := t.Tx.WriteMemory(ctx, in)
		if err != nil {
			return WriteMemoryResp{}, err
		}
		if on != nil {
			data, err := json.Marshal(map[string]any{"scope": req.Scope, "key": req.Key})
			if err != nil {
				return WriteMemoryResp{}, err
			}
			t.Event(store.EventInput{TaskID: on.ID, BoardID: on.BoardID, Type: domain.EventMemoryWritten, Data: data})
			if t.Task == nil {
				t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: on.ID, BoardID: on.BoardID})
			}
		}
		return WriteMemoryResp{EntryID: e.ID}, nil
	})
}

// ReadMemory is read_memory: the task's newest 200 task, agent and shared entries, then the scope,
// key, agent-privacy and TTL filters (store.Memory.ReadMemory's window-then-filter).
func (s contextSvc) ReadMemory(ctx context.Context, caller domain.Caller, req ReadMemoryReq) (ReadMemoryResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "read_memory", domain.GrantSubject{}, true); err != nil {
		return ReadMemoryResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ReadMemoryResp{}, err
	}
	if req.Scope != "" && !slices.Contains(ReadMemoryScopeValues, req.Scope) {
		return ReadMemoryResp{}, domain.Validation("read_memory reads the task scopes (task, agent, shared), not "+string(req.Scope), map[string]any{"field": "scope"})
	}
	entries, err := s.c.store.ReadMemory(ctx, req.ProjectID, req.TaskID, store.ReadMemoryQuery{Scope: store.MemoryScope(req.Scope), Key: req.Key, Identity: caller.Identity})
	if err != nil {
		return ReadMemoryResp{}, s.c.mapErr("read_memory", err)
	}
	out, err := memoryOut(entries)
	if err != nil {
		return ReadMemoryResp{}, s.c.mapErr("read_memory", err)
	}
	return ReadMemoryResp{Entries: out}, nil
}

// RecallMemory is recall_memory: an exact key, or the newest k with no key, the expired dropped (no
// FTS; plan §Clarifications 1). project memory is projectId's, agent_global the CALLER's, never
// anything named by the arguments; scope omitted reads both, merged newest first and cut to k.
// The tool is not tenant-scoped (agent_global needs no project), so authorize checks no membership:
// naming a project requires membership in it, checked here, on every branch, so a non-member
// cannot probe with it (conductor's loadMembership).
func (s contextSvc) RecallMemory(ctx context.Context, caller domain.Caller, req RecallMemoryReq) (RecallMemoryResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "recall_memory", domain.GrantSubject{}, false); err != nil {
		return RecallMemoryResp{}, err
	}
	if req.Scope != "" && !slices.Contains(RecallMemoryScopeValues, req.Scope) {
		return RecallMemoryResp{}, domain.Validation("recall_memory reads the durable scopes (project, agent_global), not "+string(req.Scope), map[string]any{"field": "scope"})
	}
	k := int(req.K)
	switch {
	case req.K == 0:
		k = recallDefaultK
	case req.K < 0 || req.K > recallMaxK:
		return RecallMemoryResp{}, domain.Validation("k is 1 to 100", map[string]any{"field": "k"})
	}
	if req.Key != "" {
		if err := maxChars("key", req.Key, maxKeyChars); err != nil {
			return RecallMemoryResp{}, err
		}
	}
	if req.ProjectID != "" {
		e, err := s.c.accessEntry(ctx, req.ProjectID, caller.Identity)
		if err != nil {
			return RecallMemoryResp{}, err
		}
		if !e.member {
			return RecallMemoryResp{}, forbidden("Not a member of project "+req.ProjectID, map[string]any{"projectId": req.ProjectID})
		}
	} else if req.Scope != "agent_global" {
		return RecallMemoryResp{}, domain.Validation("recall_memory needs a projectId unless scope is 'agent_global'", map[string]any{"field": "projectId"})
	}
	entries, err := s.c.recallDurable(ctx, caller.Identity, req.ProjectID, req.Scope, req.Key, k)
	if err != nil {
		return RecallMemoryResp{}, s.c.mapErr("recall_memory", err)
	}
	out, err := memoryOut(entries)
	if err != nil {
		return RecallMemoryResp{}, s.c.mapErr("recall_memory", err)
	}
	return RecallMemoryResp{Entries: out}, nil
}

// recallDurable is conductor's recallDurable (context.ts:44-58), shared by recall_memory and
// get_task_context's includeProjectMemory so the two cannot diverge: projectID's project memory
// unless scope is agent_global, the identity's agent_global memory unless scope is project, newest
// first, at most k.
func (c *Core) recallDurable(ctx context.Context, identity, projectID string, scope RecallMemoryScope, key string, k int) ([]store.MemoryEntry, error) {
	var out []store.MemoryEntry
	if projectID != "" && scope != "agent_global" {
		rows, err := c.store.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: projectID, Key: key, Limit: k})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	if scope != "project" {
		rows, err := c.store.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryAgentGlobal, Identity: identity, Key: key, Limit: k})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	slices.SortStableFunc(out, func(a, b store.MemoryEntry) int {
		return cmp.Or(cmp.Compare(b.TS, a.TS), strings.Compare(b.ID, a.ID))
	})
	return out[:min(len(out), k)], nil
}

func memoryOut(entries []store.MemoryEntry) ([]MemoryEntry, error) {
	out := make([]MemoryEntry, 0, len(entries))
	for _, e := range entries {
		m := MemoryEntry{Author: e.Author, ID: e.ID, Key: e.Key, Scope: domain.MemoryScope(e.Scope), Ts: isoMs(e.TS)}
		if e.TaskID != "" {
			id := e.TaskID
			m.TaskID = &id
		}
		if e.ExpiresAt != 0 {
			ttl := isoMs(e.ExpiresAt)
			m.TTL = &ttl
		}
		// TODO(spec): gcsUri names a blob once Blobs is built; a blob-backed entry reads as null.
		if !isNullJSON(e.Value) {
			if err := json.Unmarshal(e.Value, &m.Value); err != nil {
				return nil, internalf("memory %s value is not valid JSON: %v", e.ID, err)
			}
		}
		out = append(out, m)
	}
	return out, nil
}
