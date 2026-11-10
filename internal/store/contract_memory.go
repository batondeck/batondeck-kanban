package store

import (
	"context"
	"encoding/json"
)

// MemoryScope is where a memory entry lives and who sees it.
type MemoryScope string

const (
	// MemoryTask, MemoryAgent and MemoryShared live under a task and go with it. An agent entry is
	// visible only to its author.
	MemoryTask   MemoryScope = "task"
	MemoryAgent  MemoryScope = "agent"
	MemoryShared MemoryScope = "shared"
	// MemoryProject lives under the project, visible to every member.
	MemoryProject MemoryScope = "project"
	// MemoryAgentGlobal lives under the author's identity, across all its projects, private to it.
	MemoryAgentGlobal MemoryScope = "agent_global"
)

// MemoryEntry is one memory row. ProjectID is "" for an agent_global entry. TaskID is the task a
// task-scoped entry lives under; on a project entry it is provenance ("learned on T-3") or "".
// Value is the entry's JSON, nil for null. BlobSHA256 names a blob holding a large value, or "".
// TS and ExpiresAt are Unix milliseconds; ExpiresAt 0 never expires.
type MemoryEntry struct {
	ID         string
	ProjectID  string
	TaskID     string
	Scope      MemoryScope
	Key        string
	Value      json.RawMessage
	BlobSHA256 string
	Author     string
	TS         int64
	ExpiresAt  int64
}

// WriteMemoryInput is an entry to write. Scope routes it: task, agent and shared need TaskID;
// project needs ProjectID (TaskID optional, provenance); agent_global belongs to Author and keeps no
// ProjectID or TaskID (both are ignored). ExpiresAt is the caller's TTL as an absolute time, 0 for
// none.
type WriteMemoryInput struct {
	ProjectID  string
	TaskID     string
	Scope      MemoryScope
	Key        string
	Value      json.RawMessage
	BlobSHA256 string
	Author     string
	ExpiresAt  int64
}

// ReadMemoryQuery filters a task's entries. Scope "" is all three task scopes; Key "" is every key.
// Identity is the reader: agent entries of other authors are not returned. Limit <= 0 is 200 and a
// limit above 200 is 200; it counts only entries that pass the filters (Memory.ReadMemory).
type ReadMemoryQuery struct {
	Scope    MemoryScope
	Key      string
	Identity string
	Limit    int
}

// RecallMemoryQuery reads durable memory: Scope project reads ProjectID's entries, Scope
// agent_global reads Identity's. Key "" is every key, else that key exactly. Limit (conductor's k)
// is 1 to 1000.
type RecallMemoryQuery struct {
	Scope     MemoryScope
	ProjectID string
	Identity  string
	Key       string
	Limit     int
}

// Memory is the memory area: five scopes and a TTL. Keys: a task entry is under (project_id,
// task_id), a project entry under project_id, an agent_global entry under its author; an entry's id
// is unique on its own. Every read leaves out an entry whose ExpiresAt has passed, judged at the
// read, before any sweep has run (DeleteExpiredMemory is the sweep, run by retention). Errors: a
// missing task is domain.NotFound("Task", id), a missing project domain.NotFound("Project", id), a
// BlobSHA256 naming no blob domain.NotFound("Blob", sha256); an
// unknown scope, a task scope without a TaskID, a project scope without a ProjectID, an empty key or
// author, a read of a durable scope through ReadMemory and a recall limit outside 1..1000 are
// domain.Validation. Writes mint a new entry every call: there is no upsert on key. The area is
// versionless. Lists are newest first (ts, then id), bounded by their limit, never paginated.
type Memory interface {
	// WriteMemory stores a new entry, stamped with a new id and the current time.
	WriteMemory(ctx context.Context, in WriteMemoryInput) (MemoryEntry, error)
	// ReadMemory is filter first, then window: of the task's task, agent and shared entries it keeps
	// the live ones matching q.Scope and q.Key that q.Identity may see (another author's agent entry
	// is not), and returns the newest q.Limit of those. An entry the reader cannot see never takes a
	// slot, so it cannot hide an older one the reader can. DEVIATION: conductor (store.ts readMemory)
	// cuts the window first and filters it after.
	ReadMemory(ctx context.Context, projectID, taskID string, q ReadMemoryQuery) ([]MemoryEntry, error)
	// RecallMemory is the durable entries of one project or one identity, newest first, at most
	// q.Limit, as conductor's recallFrom reads them. With no Key it takes the newest q.Limit entries
	// first and then leaves out the expired ones, so it can return fewer than q.Limit. With a Key it
	// is the live entries of that key, at most q.Limit. A missing project or an identity with nothing
	// recalls an empty list: recall reads one scope key and checks nothing else (the caller has
	// authorised the project).
	RecallMemory(ctx context.Context, q RecallMemoryQuery) ([]MemoryEntry, error)
	// DeleteExpiredMemory deletes at most limit entries whose ExpiresAt is at or before now and
	// returns how many it deleted. limit <= 0 is domain.Validation.
	DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error)
	// DeleteMemoryByTask deletes the task's task, agent and shared entries and returns how many; a
	// task with none is 0 and no error. Project entries naming the task as provenance are kept.
	DeleteMemoryByTask(ctx context.Context, projectID, taskID string) (int64, error)
}

var (
	_ Memory = (*SQLite)(nil)
	_ Memory = (*Postgres)(nil)
)
