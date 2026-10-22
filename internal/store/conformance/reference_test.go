package conformance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// TestContractIIBatteriesOnAReference runs the twelve batteries of store contract II against
// reference, a map-backed implementation of their areas plus a seeder. Until an engine implements
// an area its battery skips on that engine, so this is the run that proves each battery passes
// a store that keeps the contract, and fails one that breaks it (mutation proofs on the reference).
// It goes when memstore carries these areas and an engine passes them (R0.18, R0.10).
func TestContractIIBatteriesOnAReference(t *testing.T) {
	newRef := func(*testing.T) Migratable { return &reference{st: newRefState()} }
	for _, battery := range []func(*testing.T, Factory){
		contextItems, memory, summaries, dependencies, subtasks, auditLog, toolCalls,
		agentSessions, wakeSessions, attachments, blobs, search,
	} {
		battery(t, newRef)
	}
}

// reference keeps the contract II areas in maps. The embedded Store is nil: an area outside
// contract II panics if called, which no battery here does.
type reference struct {
	store.Store
	st   *refState
	inTx bool
}

type refTask struct {
	projectID, id, boardID, title, desc, parent string
	status                                      domain.Status
	version                                     int64
	blockedBy, blocks                           []string
}

type refState struct {
	seq         int
	projects    map[string][]string // project id -> board ids
	counters    map[string]int
	tasks       map[[2]string]refTask
	context     []store.ContextItem
	memory      []store.MemoryEntry
	summaries   map[[2]string]store.Summary
	edges       []store.DependencyEdge
	audit       []store.AuditRow
	toolCalls   []store.ToolCall
	agents      map[[2]string]store.AgentSession
	wake        map[string]store.WakeSession
	attachments []store.Attachment
	blobs       map[string]store.Blob
}

func newRefState() *refState {
	return &refState{
		projects: map[string][]string{}, counters: map[string]int{}, tasks: map[[2]string]refTask{},
		summaries: map[[2]string]store.Summary{}, agents: map[[2]string]store.AgentSession{},
		wake: map[string]store.WakeSession{}, blobs: map[string]store.Blob{},
	}
}

func (st *refState) clone() *refState {
	c := *st
	c.projects = maps.Clone(st.projects)
	c.counters = maps.Clone(st.counters)
	c.tasks = map[[2]string]refTask{}
	for k, v := range st.tasks {
		v.blockedBy, v.blocks = slices.Clone(v.blockedBy), slices.Clone(v.blocks)
		c.tasks[k] = v
	}
	c.context, c.memory, c.edges = slices.Clone(st.context), slices.Clone(st.memory), slices.Clone(st.edges)
	c.audit, c.toolCalls, c.attachments = slices.Clone(st.audit), slices.Clone(st.toolCalls), slices.Clone(st.attachments)
	c.summaries, c.wake, c.blobs = maps.Clone(st.summaries), maps.Clone(st.wake), maps.Clone(st.blobs)
	c.agents = map[[2]string]store.AgentSession{}
	for k, v := range st.agents {
		v.ToolCounts = maps.Clone(v.ToolCounts)
		c.agents[k] = v
	}
	return &c
}

func (r *reference) id(prefix string) string {
	r.st.seq++
	return fmt.Sprintf("%s%06d", prefix, r.st.seq)
}

func now() int64 { return time.Now().UnixMilli() }

func taskNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "T-"))
	return n
}

func (r *reference) task(projectID, taskID string) (refTask, error) {
	tk, ok := r.st.tasks[[2]string{projectID, taskID}]
	if !ok {
		return refTask{}, domain.NotFound("Task", taskID)
	}
	return tk, nil
}

func invalid(msg string) error { return domain.Validation(msg, nil) }

// cursor is base64url JSON {order, id}.
type refCursor struct {
	Order string `json:"order"`
	ID    string `json:"id"`
}

func encodeCursor(order, id string) string {
	b, _ := json.Marshal(refCursor{order, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (refCursor, error) {
	var c refCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.ID == "" {
		return c, invalid("malformed cursor")
	}
	return c, nil
}

func clamp(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	return min(limit, max)
}

// --- Lifecycle ---

func (r *reference) Migrate(context.Context) (store.Migrated, error) { return store.Migrated{}, nil }
func (r *reference) MigrateDown(context.Context) error               { return nil }
func (r *reference) SchemaCurrent(context.Context) error             { return nil }
func (r *reference) ServerVersion(context.Context) (string, error)   { return "reference", nil }
func (r *reference) Close() error                                    { return nil }

func (r *reference) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	if r.inTx {
		return fn(r)
	}
	snap := r.st.clone()
	r.inTx = true
	err := fn(r)
	r.inTx = false
	if err != nil {
		r.st = snap
	}
	return err
}

// --- seeder ---

func (r *reference) seedProject(ctx context.Context) (string, string, error) {
	p := r.id("P-")
	r.st.projects[p] = nil
	b, err := r.seedBoard(ctx, p)
	return p, b, err
}

func (r *reference) seedBoard(_ context.Context, projectID string) (string, error) {
	b := r.id("B-")
	r.st.projects[projectID] = append(r.st.projects[projectID], b)
	return b, nil
}

func (r *reference) seedTask(_ context.Context, in seedTaskInput) (string, error) {
	r.st.counters[in.ProjectID]++
	id := "T-" + strconv.Itoa(r.st.counters[in.ProjectID])
	status := in.Status
	if status == "" {
		status = domain.Ready
	}
	r.st.tasks[[2]string{in.ProjectID, id}] = refTask{projectID: in.ProjectID, id: id, boardID: in.BoardID, title: in.Title, desc: in.Description, parent: in.ParentTaskID, status: status, version: 1, blockedBy: []string{}, blocks: []string{}}
	return id, nil
}

func (r *reference) taskState(_ context.Context, projectID, taskID string) (seededTask, error) {
	tk, err := r.task(projectID, taskID)
	return seededTask{Version: tk.version, BlockedBy: slices.Clone(tk.blockedBy), Blocks: slices.Clone(tk.blocks)}, err
}

// --- Context ---

func (r *reference) AddContextItem(_ context.Context, in store.AddContextItemInput) (store.ContextItem, error) {
	if !slices.Contains([]store.ContextKind{store.ContextNote, store.ContextDecision, store.ContextField, store.ContextLink}, in.Kind) || in.Body == "" || in.Author == "" {
		return store.ContextItem{}, invalid("bad context item")
	}
	if _, err := r.task(in.ProjectID, in.TaskID); err != nil {
		return store.ContextItem{}, err
	}
	it := store.ContextItem{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: r.id("CI-"), Kind: in.Kind, Body: in.Body, Author: in.Author, TS: now()}
	r.st.context = append(r.st.context, it)
	return it, nil
}

func (r *reference) ListContextItems(_ context.Context, projectID, taskID string, q store.ContextItemsQuery) (store.ContextItemsPage, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return store.ContextItemsPage{}, err
	}
	var items []store.ContextItem
	for _, it := range r.st.context {
		if it.ProjectID == projectID && it.TaskID == taskID {
			items = append(items, it)
		}
	}
	key := func(it store.ContextItem) string { return fmt.Sprintf("%020d/%s", it.TS, it.ID) }
	slices.SortFunc(items, func(a, b store.ContextItem) int { return strings.Compare(key(a), key(b)) })
	if !q.Ascending {
		slices.Reverse(items)
	}
	if q.Cursor != "" {
		c, err := decodeCursor(q.Cursor)
		if err != nil {
			return store.ContextItemsPage{}, err
		}
		ts, _ := strconv.ParseInt(c.Order, 10, 64)
		at := fmt.Sprintf("%020d/%s", ts, c.ID)
		items = slices.DeleteFunc(items, func(it store.ContextItem) bool {
			if q.Ascending {
				return key(it) <= at
			}
			return key(it) >= at
		})
	}
	limit := clamp(q.Limit, 50, 200)
	page := store.ContextItemsPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
	}
	return page, nil
}

func (r *reference) DeleteContextItemsByTask(_ context.Context, projectID, taskID string) (int64, error) {
	before := len(r.st.context)
	r.st.context = slices.DeleteFunc(r.st.context, func(it store.ContextItem) bool { return it.ProjectID == projectID && it.TaskID == taskID })
	return int64(before - len(r.st.context)), nil
}

// --- Memory ---

func taskScope(s store.MemoryScope) bool {
	return s == store.MemoryTask || s == store.MemoryAgent || s == store.MemoryShared
}

func live(e store.MemoryEntry, at int64) bool { return e.ExpiresAt == 0 || e.ExpiresAt > at }

func newestFirst(es []store.MemoryEntry) {
	slices.SortFunc(es, func(a, b store.MemoryEntry) int {
		if a.TS != b.TS {
			return int(b.TS - a.TS)
		}
		return strings.Compare(b.ID, a.ID)
	})
}

func (r *reference) WriteMemory(_ context.Context, in store.WriteMemoryInput) (store.MemoryEntry, error) {
	if in.Key == "" || in.Author == "" {
		return store.MemoryEntry{}, invalid("empty key or author")
	}
	e := store.MemoryEntry{ID: r.id("M-"), Scope: in.Scope, Key: in.Key, Value: in.Value, BlobSHA256: in.BlobSHA256, Author: in.Author, TS: now(), ExpiresAt: in.ExpiresAt}
	switch {
	case taskScope(in.Scope):
		if in.TaskID == "" {
			return store.MemoryEntry{}, invalid("a task scope needs a task")
		}
		if _, err := r.task(in.ProjectID, in.TaskID); err != nil {
			return store.MemoryEntry{}, err
		}
		e.ProjectID, e.TaskID = in.ProjectID, in.TaskID
	case in.Scope == store.MemoryProject:
		if in.ProjectID == "" {
			return store.MemoryEntry{}, invalid("a project scope needs a project")
		}
		if _, ok := r.st.projects[in.ProjectID]; !ok {
			return store.MemoryEntry{}, domain.NotFound("Project", in.ProjectID)
		}
		e.ProjectID, e.TaskID = in.ProjectID, in.TaskID
	case in.Scope == store.MemoryAgentGlobal:
	default:
		return store.MemoryEntry{}, invalid("unknown scope")
	}
	if e.BlobSHA256 != "" {
		if _, ok := r.st.blobs[e.BlobSHA256]; !ok {
			return store.MemoryEntry{}, domain.NotFound("Blob", e.BlobSHA256)
		}
	}
	r.st.memory = append(r.st.memory, e)
	return e, nil
}

func (r *reference) ReadMemory(_ context.Context, projectID, taskID string, q store.ReadMemoryQuery) ([]store.MemoryEntry, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return nil, err
	}
	if q.Scope != "" && !taskScope(q.Scope) {
		return nil, invalid("ReadMemory reads the task scopes")
	}
	at := now()
	var out []store.MemoryEntry
	for _, e := range r.st.memory {
		if e.ProjectID == projectID && e.TaskID == taskID && taskScope(e.Scope) && live(e, at) &&
			(q.Scope == "" || e.Scope == q.Scope) && (q.Key == "" || e.Key == q.Key) &&
			(e.Scope != store.MemoryAgent || e.Author == q.Identity) {
			out = append(out, e)
		}
	}
	newestFirst(out)
	return out[:min(len(out), clamp(q.Limit, 200, 200))], nil
}

func (r *reference) RecallMemory(_ context.Context, q store.RecallMemoryQuery) ([]store.MemoryEntry, error) {
	if q.Limit < 1 || q.Limit > 1000 || (q.Scope != store.MemoryProject && q.Scope != store.MemoryAgentGlobal) {
		return nil, invalid("bad recall")
	}
	at := now()
	var out []store.MemoryEntry
	for _, e := range r.st.memory {
		mine := (q.Scope == store.MemoryProject && e.Scope == store.MemoryProject && e.ProjectID == q.ProjectID) ||
			(q.Scope == store.MemoryAgentGlobal && e.Scope == store.MemoryAgentGlobal && e.Author == q.Identity)
		if mine && live(e, at) && (q.Key == "" || e.Key == q.Key) {
			out = append(out, e)
		}
	}
	newestFirst(out)
	return out[:min(len(out), q.Limit)], nil
}

func (r *reference) DeleteExpiredMemory(_ context.Context, at int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, invalid("limit")
	}
	var n int64
	r.st.memory = slices.DeleteFunc(r.st.memory, func(e store.MemoryEntry) bool {
		if n < int64(limit) && !live(e, at+1) {
			n++
			return true
		}
		return false
	})
	return n, nil
}

func (r *reference) DeleteMemoryByTask(_ context.Context, projectID, taskID string) (int64, error) {
	before := len(r.st.memory)
	r.st.memory = slices.DeleteFunc(r.st.memory, func(e store.MemoryEntry) bool {
		return e.ProjectID == projectID && e.TaskID == taskID && taskScope(e.Scope)
	})
	return int64(before - len(r.st.memory)), nil
}

// --- Summaries ---

func (r *reference) SetSummary(_ context.Context, projectID, taskID, body, updatedBy string, expected int64) (store.Summary, error) {
	tk, err := r.task(projectID, taskID)
	if err != nil {
		return store.Summary{}, err
	}
	if tk.version != expected {
		return store.Summary{}, domain.Stale(expected, tk.version)
	}
	s := store.Summary{ProjectID: projectID, TaskID: taskID, Body: body, UpdatedBy: updatedBy, UpdatedAt: now()}
	r.st.summaries[[2]string{projectID, taskID}] = s
	return s, nil
}

func (r *reference) GetSummary(_ context.Context, projectID, taskID string) (store.Summary, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return store.Summary{}, err
	}
	s, ok := r.st.summaries[[2]string{projectID, taskID}]
	if !ok {
		s = store.Summary{ProjectID: projectID, TaskID: taskID}
	}
	return s, nil
}

// --- Dependencies ---

func (r *reference) AddEdge(_ context.Context, in store.AddEdgeInput) (store.DependencyEdge, error) {
	if (in.Type != store.DependencyBlocks && in.Type != store.DependencyRelates) || in.CreatedBy == "" {
		return store.DependencyEdge{}, invalid("bad edge")
	}
	from, err := r.task(in.ProjectID, in.FromTaskID)
	if err != nil {
		return store.DependencyEdge{}, err
	}
	to, err := r.task(in.ProjectID, in.ToTaskID)
	if err != nil {
		return store.DependencyEdge{}, err
	}
	var blocking []domain.Edge
	for _, e := range r.st.edges {
		if e.ProjectID == in.ProjectID && e.FromTaskID == in.FromTaskID && e.ToTaskID == in.ToTaskID && e.Type == in.Type {
			return e, nil
		}
		if e.ProjectID == in.ProjectID && e.Type == store.DependencyBlocks {
			blocking = append(blocking, domain.Edge{FromTaskID: e.FromTaskID, ToTaskID: e.ToTaskID})
		}
	}
	if in.Type == store.DependencyBlocks {
		if err := domain.CheckCycle(blocking, in.FromTaskID, in.ToTaskID); err != nil {
			return store.DependencyEdge{}, err
		}
		if !slices.Contains(to.blockedBy, from.id) {
			to.blockedBy = append(to.blockedBy, from.id)
		}
		if !slices.Contains(from.blocks, to.id) {
			from.blocks = append(from.blocks, to.id)
		}
		r.st.tasks[[2]string{in.ProjectID, to.id}] = to
		r.st.tasks[[2]string{in.ProjectID, from.id}] = from
	}
	e := store.DependencyEdge{ProjectID: in.ProjectID, ID: r.id("D-"), FromTaskID: in.FromTaskID, ToTaskID: in.ToTaskID, Type: in.Type, CreatedBy: in.CreatedBy, TS: now()}
	r.st.edges = append(r.st.edges, e)
	return e, nil
}

func (r *reference) RemoveEdge(_ context.Context, projectID, edgeID string) (store.DependencyEdge, error) {
	i := slices.IndexFunc(r.st.edges, func(e store.DependencyEdge) bool { return e.ProjectID == projectID && e.ID == edgeID })
	if i < 0 {
		return store.DependencyEdge{}, domain.NotFound("DependencyEdge", edgeID)
	}
	e := r.st.edges[i]
	r.st.edges = slices.Delete(r.st.edges, i, i+1)
	if e.Type == store.DependencyBlocks {
		if to, err := r.task(projectID, e.ToTaskID); err == nil {
			to.blockedBy = slices.DeleteFunc(to.blockedBy, func(id string) bool { return id == e.FromTaskID })
			r.st.tasks[[2]string{projectID, to.id}] = to
		}
		if from, err := r.task(projectID, e.FromTaskID); err == nil {
			from.blocks = slices.DeleteFunc(from.blocks, func(id string) bool { return id == e.ToTaskID })
			r.st.tasks[[2]string{projectID, from.id}] = from
		}
	}
	return e, nil
}

func (r *reference) edgesWhere(keep func(store.DependencyEdge) bool) []store.DependencyEdge {
	out := []store.DependencyEdge{}
	for _, e := range r.st.edges {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func (r *reference) ListEdges(_ context.Context, projectID string) ([]store.DependencyEdge, error) {
	if _, ok := r.st.projects[projectID]; !ok {
		return nil, domain.NotFound("Project", projectID)
	}
	return r.edgesWhere(func(e store.DependencyEdge) bool { return e.ProjectID == projectID }), nil
}

func (r *reference) ListBlockers(_ context.Context, projectID, taskID string) ([]store.DependencyEdge, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return nil, err
	}
	return r.edgesWhere(func(e store.DependencyEdge) bool { return e.ProjectID == projectID && e.ToTaskID == taskID }), nil
}

func (r *reference) ListDependents(_ context.Context, projectID, taskID string) ([]store.DependencyEdge, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return nil, err
	}
	return r.edgesWhere(func(e store.DependencyEdge) bool { return e.ProjectID == projectID && e.FromTaskID == taskID }), nil
}

func (r *reference) SetCaches(_ context.Context, projectID, taskID string, blockedBy, blocks []string) error {
	tk, err := r.task(projectID, taskID)
	if err != nil {
		return err
	}
	tk.blockedBy, tk.blocks = append([]string{}, blockedBy...), append([]string{}, blocks...)
	r.st.tasks[[2]string{projectID, taskID}] = tk
	return nil
}

// --- Subtasks ---

func (r *reference) ListSubtasks(_ context.Context, projectID, parentTaskID string) ([]string, error) {
	if _, err := r.task(projectID, parentTaskID); err != nil {
		return nil, err
	}
	out := []string{}
	for _, tk := range r.st.tasks {
		if tk.projectID == projectID && tk.parent == parentTaskID {
			out = append(out, tk.id)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return taskNum(a) - taskNum(b) })
	return out, nil
}

// --- Audit ---

func (r *reference) AppendAudit(_ context.Context, row store.AuditRow) (int64, error) {
	var obj map[string]any
	if row.Action == "" || row.Actor == "" || (row.Result != store.AuditOK && row.Result != store.AuditError) ||
		(row.Data != nil && json.Unmarshal(row.Data, &obj) != nil) {
		return 0, invalid("bad audit row")
	}
	row.ID = int64(len(r.st.audit) + 1)
	if row.Data == nil {
		row.Data = json.RawMessage(`{}`)
	}
	r.st.audit = append(r.st.audit, row)
	return row.ID, nil
}

func (r *reference) ListAuditLog(_ context.Context, f store.AuditFilter, cursor string, limit int) (store.AuditPage, error) {
	var after *refCursor
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return store.AuditPage{}, err
		}
		after = &c
	}
	key := func(a store.AuditRow) string { return fmt.Sprintf("%020d/%020d", a.TS, a.ID) }
	var rows []store.AuditRow
	for _, a := range r.st.audit {
		match := (f.ProjectID == "" || a.ProjectID == f.ProjectID) && (f.Actor == "" || a.Actor == f.Actor) &&
			(f.Action == "" || a.Action == f.Action) && (f.Subject == "" || a.Subject == f.Subject) &&
			(f.Since == 0 || a.TS >= f.Since) && (f.Until == 0 || a.TS <= f.Until)
		if match && after != nil {
			ts, _ := strconv.ParseInt(after.Order, 10, 64)
			id, _ := strconv.ParseInt(after.ID, 10, 64)
			match = key(a) < fmt.Sprintf("%020d/%020d", ts, id)
		}
		if match {
			rows = append(rows, a)
		}
	}
	slices.SortFunc(rows, func(a, b store.AuditRow) int { return strings.Compare(key(b), key(a)) })
	limit = clamp(limit, 50, 500)
	page := store.AuditPage{Rows: rows}
	if len(rows) > limit {
		page.Rows = rows[:limit]
		last := page.Rows[limit-1]
		page.NextCursor = encodeCursor(strconv.FormatInt(last.TS, 10), strconv.FormatInt(last.ID, 10))
	}
	return page, nil
}

// --- ToolCalls ---

func (r *reference) InsertToolCalls(_ context.Context, batch []store.ToolCall) error {
	for _, c := range batch {
		if c.Tool == "" || c.Identity == "" || c.Code == "" || c.LatencyMs < 0 || (c.Outcome != store.ToolCallOK && c.Outcome != store.ToolCallError) {
			return invalid("bad tool call")
		}
	}
	r.st.toolCalls = append(r.st.toolCalls, batch...)
	return nil
}

func (r *reference) DeleteToolCallsOlderThan(_ context.Context, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, invalid("limit")
	}
	slices.SortStableFunc(r.st.toolCalls, func(a, b store.ToolCall) int { return int(a.TS - b.TS) })
	var n int64
	r.st.toolCalls = slices.DeleteFunc(r.st.toolCalls, func(c store.ToolCall) bool {
		if n < int64(limit) && c.TS < before {
			n++
			return true
		}
		return false
	})
	return n, nil
}

// --- AgentSessions ---

func (r *reference) UpsertAgentSession(_ context.Context, in store.AgentSessionStart) (store.AgentSession, error) {
	if in.IdentityID == "" || in.AgentID == "" || in.AgentName == "" || in.Kind == "" {
		return store.AgentSession{}, invalid("bad session")
	}
	k := [2]string{in.IdentityID, in.AgentID}
	s, ok := r.st.agents[k]
	if !ok {
		s = store.AgentSession{IdentityID: in.IdentityID, AgentID: in.AgentID, FirstSeenAt: in.At, ToolCounts: map[string]int64{}}
	}
	s.AgentName, s.Kind, s.LastSeenAt = in.AgentName, in.Kind, in.At
	s.SessionCount++
	for _, f := range []struct {
		dst *string
		v   string
	}{{&s.ClientName, in.ClientName}, {&s.ClientVersion, in.ClientVersion}, {&s.OAuthClientID, in.OAuthClientID}} {
		if f.v != "" {
			*f.dst = f.v
		}
	}
	r.st.agents[k] = s
	s.ToolCounts = maps.Clone(s.ToolCounts)
	return s, nil
}

func (r *reference) TouchAgentSession(_ context.Context, in store.AgentSessionTouch) error {
	if in.IdentityID == "" || in.AgentID == "" || in.AgentName == "" || in.Kind == "" || in.Tool == "" || in.Count < 0 {
		return invalid("bad touch")
	}
	k := [2]string{in.IdentityID, in.AgentID}
	s, ok := r.st.agents[k]
	if !ok {
		s = store.AgentSession{IdentityID: in.IdentityID, AgentID: in.AgentID, FirstSeenAt: in.At, ToolCounts: map[string]int64{}}
	}
	n := max(in.Count, 1)
	s.AgentName, s.Kind, s.LastSeenAt, s.LastTool = in.AgentName, in.Kind, in.At, in.Tool
	s.RequestCount += n
	s.ToolCounts[in.Tool] += n
	if in.ProjectID != "" {
		s.LastProjectID = in.ProjectID
	}
	r.st.agents[k] = s
	return nil
}

func (r *reference) DeleteAgentSession(_ context.Context, identityID, agentID string) error {
	k := [2]string{identityID, agentID}
	if _, ok := r.st.agents[k]; !ok {
		return domain.NotFound("AgentSession", agentID)
	}
	delete(r.st.agents, k)
	return nil
}

func (r *reference) ListAgentSessions(_ context.Context, identityID string, limit int) ([]store.AgentSession, error) {
	var out []store.AgentSession
	for _, s := range r.st.agents {
		if identityID == "" || s.IdentityID == identityID {
			s.ToolCounts = maps.Clone(s.ToolCounts)
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b store.AgentSession) int {
		if a.LastSeenAt != b.LastSeenAt {
			return int(b.LastSeenAt - a.LastSeenAt)
		}
		return strings.Compare(a.AgentID, b.AgentID)
	})
	return out[:min(len(out), clamp(limit, 100, 1000))], nil
}

func (r *reference) DeleteIdleAgentSessions(_ context.Context, before int64) (int64, error) {
	var n int64
	for k, s := range r.st.agents {
		if s.LastSeenAt < before {
			delete(r.st.agents, k)
			n++
		}
	}
	return n, nil
}

// --- WakeSessions ---

func (r *reference) PutWakeSession(_ context.Context, s store.WakeSession) error {
	if s.ID == "" || s.IdentityID == "" || s.AgentID == "" || s.ExpiresAt == 0 {
		return invalid("bad wake session")
	}
	if old, ok := r.st.wake[s.ID]; ok {
		s.CreatedAt = old.CreatedAt
	}
	r.st.wake[s.ID] = s
	return nil
}

func (r *reference) GetWakeSession(_ context.Context, id string) (store.WakeSession, error) {
	s, ok := r.st.wake[id]
	if !ok || s.ExpiresAt <= now() {
		return store.WakeSession{}, domain.NotFound("WakeSession", id)
	}
	return s, nil
}

func (r *reference) DeleteWakeSession(_ context.Context, id string) error {
	if _, ok := r.st.wake[id]; !ok {
		return domain.NotFound("WakeSession", id)
	}
	delete(r.st.wake, id)
	return nil
}

// --- Attachments ---

func (r *reference) CreateAttachment(_ context.Context, in store.CreateAttachmentInput) (store.Attachment, error) {
	if !slices.Contains([]store.AttachmentKind{store.AttachmentFile, store.AttachmentImage, store.AttachmentAudio, store.AttachmentVideo}, in.Kind) || in.MimeType == "" || in.CreatedBy == "" || in.Bytes < 0 {
		return store.Attachment{}, invalid("bad attachment")
	}
	if _, err := r.task(in.ProjectID, in.TaskID); err != nil {
		return store.Attachment{}, err
	}
	a := store.Attachment{ProjectID: in.ProjectID, TaskID: in.TaskID, ID: r.id("A-"), MimeType: in.MimeType, Bytes: in.Bytes, Kind: in.Kind, Status: store.AttachmentPending, CreatedBy: in.CreatedBy, TS: now()}
	r.st.attachments = append(r.st.attachments, a)
	return a, nil
}

func (r *reference) SetAttachmentReady(_ context.Context, projectID, taskID, attachmentID, sha string) (store.Attachment, error) {
	i := slices.IndexFunc(r.st.attachments, func(a store.Attachment) bool {
		return a.ProjectID == projectID && a.TaskID == taskID && a.ID == attachmentID
	})
	if i < 0 {
		return store.Attachment{}, domain.NotFound("Attachment", attachmentID)
	}
	if r.st.attachments[i].Status != store.AttachmentPending {
		return store.Attachment{}, invalid("not pending")
	}
	if _, ok := r.st.blobs[sha]; !ok {
		return store.Attachment{}, domain.NotFound("Blob", sha)
	}
	r.st.attachments[i].Status, r.st.attachments[i].BlobSHA256 = store.AttachmentReady, sha
	return r.st.attachments[i], nil
}

func (r *reference) ListAttachments(_ context.Context, projectID, taskID string) ([]store.Attachment, error) {
	if _, err := r.task(projectID, taskID); err != nil {
		return nil, err
	}
	out := []store.Attachment{}
	for _, a := range r.st.attachments {
		if a.ProjectID == projectID && a.TaskID == taskID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *reference) DeleteAttachmentsByTask(_ context.Context, projectID, taskID string) (int64, error) {
	before := len(r.st.attachments)
	r.st.attachments = slices.DeleteFunc(r.st.attachments, func(a store.Attachment) bool { return a.ProjectID == projectID && a.TaskID == taskID })
	return int64(before - len(r.st.attachments)), nil
}

// --- Blobs ---

var blobHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (r *reference) referenced(sha string) bool {
	return slices.ContainsFunc(r.st.attachments, func(a store.Attachment) bool { return a.BlobSHA256 == sha }) ||
		slices.ContainsFunc(r.st.memory, func(e store.MemoryEntry) bool { return e.BlobSHA256 == sha })
}

func (r *reference) PutBlob(_ context.Context, sha string, bytes int64) (store.Blob, error) {
	if !blobHash.MatchString(sha) || bytes < 0 {
		return store.Blob{}, invalid("bad blob")
	}
	if b, ok := r.st.blobs[sha]; ok {
		return b, nil
	}
	b := store.Blob{SHA256: sha, Bytes: bytes, CreatedAt: now()}
	r.st.blobs[sha] = b
	return b, nil
}

func (r *reference) GetBlob(_ context.Context, sha string) (store.Blob, error) {
	b, ok := r.st.blobs[sha]
	if !ok {
		return store.Blob{}, domain.NotFound("Blob", sha)
	}
	return b, nil
}

func (r *reference) DeleteBlob(_ context.Context, sha string) error {
	if _, ok := r.st.blobs[sha]; !ok {
		return domain.NotFound("Blob", sha)
	}
	if r.referenced(sha) {
		return invalid("referenced")
	}
	delete(r.st.blobs, sha)
	return nil
}

func (r *reference) ListUnreferencedBlobs(_ context.Context, olderThan int64, limit int) ([]store.Blob, error) {
	if limit <= 0 {
		return nil, invalid("limit")
	}
	var out []store.Blob
	for _, b := range r.st.blobs {
		if b.CreatedAt < olderThan && !r.referenced(b.SHA256) {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b store.Blob) int {
		if a.CreatedAt != b.CreatedAt {
			return int(a.CreatedAt - b.CreatedAt)
		}
		return strings.Compare(a.SHA256, b.SHA256)
	})
	return out[:min(len(out), limit)], nil
}

// --- Search ---

type refTerm struct {
	text   string
	prefix bool
}

// terms is the contract's query normalisation: split on every non-letter-or-digit, lowercase, a
// '*' directly after a term makes it a prefix.
func terms(query string) []refTerm {
	var out []refTerm
	rs := []rune(query)
	for i := 0; i < len(rs); {
		if !unicode.IsLetter(rs[i]) && !unicode.IsDigit(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
			j++
		}
		out = append(out, refTerm{text: strings.ToLower(string(rs[i:j])), prefix: j < len(rs) && rs[j] == '*'})
		i = j
	}
	return out
}

func (r *reference) hits(projectID, query string, f store.SearchFilter) ([]store.SearchHit, error) {
	ts := terms(query)
	if len(ts) == 0 {
		return nil, invalid("no term")
	}
	var out []store.SearchHit
	for _, tk := range r.st.tasks {
		if tk.projectID != projectID || (f.BoardID != "" && tk.boardID != f.BoardID) || (f.Statuses != nil && !slices.Contains(f.Statuses, tk.status)) {
			continue
		}
		var tokens []string
		for _, t := range terms(tk.title + " " + tk.desc) {
			tokens = append(tokens, t.text)
		}
		rank := 0
		for _, q := range ts {
			n := 0
			for _, tok := range tokens {
				if tok == q.text || (q.prefix && strings.HasPrefix(tok, q.text)) {
					n++
				}
			}
			if n == 0 {
				rank = -1
				break
			}
			rank += n
		}
		if rank > 0 {
			out = append(out, store.SearchHit{TaskID: tk.id, Rank: float64(rank)})
		}
	}
	slices.SortFunc(out, func(a, b store.SearchHit) int {
		if a.Rank != b.Rank {
			return int(b.Rank - a.Rank)
		}
		return taskNum(a.TaskID) - taskNum(b.TaskID)
	})
	return out, nil
}

func (r *reference) SearchTasks(_ context.Context, projectID, query string, f store.SearchFilter, cursor string, limit int) (store.SearchPage, error) {
	hits, err := r.hits(projectID, query, f)
	if err != nil {
		return store.SearchPage{}, err
	}
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return store.SearchPage{}, err
		}
		rank, _ := strconv.ParseFloat(c.Order, 64)
		hits = slices.DeleteFunc(hits, func(h store.SearchHit) bool {
			return h.Rank > rank || (h.Rank == rank && taskNum(h.TaskID) <= taskNum(c.ID))
		})
	}
	limit = clamp(limit, 10, 100)
	page := store.SearchPage{Hits: hits}
	if len(hits) > limit {
		page.Hits = hits[:limit]
		last := page.Hits[limit-1]
		page.NextCursor = encodeCursor(strconv.FormatFloat(last.Rank, 'g', -1, 64), last.TaskID)
	}
	return page, nil
}

func (r *reference) CountSearch(_ context.Context, projectID, query string, f store.SearchFilter) (int64, error) {
	hits, err := r.hits(projectID, query, f)
	return int64(len(hits)), err
}
