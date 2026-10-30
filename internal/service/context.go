package service

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// contextSvc is service.Context: items, the briefing, the summary (here), memory (memory.go),
// dependencies (dependencies.go) and subtasks (subtasks.go).
type contextSvc struct{ c *Core }

var _ Context = contextSvc{}

// Context is the per-task context service.
func (c *Core) Context() Context { return contextSvc{c} }

// Conductor's argument bounds (src/config/constants.ts), in JavaScript string length (UTF-16 code
// units), as zod measures them.
const (
	maxTitleChars       = 300
	maxDescriptionChars = 32_000
	maxSummaryChars     = 20_000
	maxKeyChars         = 200
	// contextDefaultLimit and contextMaxLimit are get_task_context's limit (default 20, max 100).
	contextDefaultLimit = 20
	contextMaxLimit     = 100
	// contextFollowUpsMax is get_task_context's openFollowUps page: the store's largest (200), so a
	// worker acting on each open follow-up sees them all; a task with more reads the oldest 200, and
	// openFollowUpCount counts that page (list_follow_ups {openOnly} pages the rest).
	contextFollowUpsMax = 200
)

func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

func maxChars(field, v string, n int) error {
	if jsLen(v) > n {
		return domain.Validation(field+" is too long", map[string]any{"field": field, "max": n})
	}
	return nil
}

// safeHTTPURL is conductor's isSafeHttpUrl (src/util/url.ts): an http(s) URL with no credentials.
func safeHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// AddItem is add_context_item: the task's version moves once (service.mutate's one bump).
func (s contextSvc) AddItem(ctx context.Context, caller domain.Caller, req AddContextItemReq) (AddContextItemResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_context_item", domain.GrantSubject{}, true)
	if err != nil {
		return AddContextItemResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return AddContextItemResp{}, err
	}
	if !slices.Contains(ContextKindValues, req.Kind) {
		return AddContextItemResp{}, domain.Validation("unknown context kind: "+string(req.Kind), map[string]any{"field": "kind"})
	}
	if err := required("body", req.Body); err != nil {
		return AddContextItemResp{}, err
	}
	if err := maxChars("body", req.Body, maxDescriptionChars); err != nil {
		return AddContextItemResp{}, err
	}
	if req.Kind == "link" && !safeHTTPURL(req.Body) {
		return AddContextItemResp{}, domain.Validation("kind 'link' requires body to be an http(s) URL", map[string]any{"body": req.Body})
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddContextItemResp{}, err
	}
	// TODO(spec): conductor's enforceTokenBudget warnings (the per-task chars budget) are not ported.
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_context_item", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (AddContextItemResp, error) {
		item, err := t.Tx.AddContextItem(ctx, store.AddContextItemInput{ProjectID: req.ProjectID, TaskID: req.TaskID, Kind: store.ContextKind(req.Kind), Body: req.Body, Author: caller.Identity})
		if err != nil {
			return AddContextItemResp{}, err
		}
		data, err := json.Marshal(map[string]any{"kind": req.Kind})
		if err != nil {
			return AddContextItemResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventContextAdded, Data: data})
		return AddContextItemResp{Item: itemOut(item)}, nil
	})
}

// SetSummary is set_summary: version-checked by mutate (STALE), the summary row written, the
// version bumped once.
func (s contextSvc) SetSummary(ctx context.Context, caller domain.Caller, req SetSummaryReq) (SetSummaryResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "set_summary", domain.GrantSubject{}, true)
	if err != nil {
		return SetSummaryResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return SetSummaryResp{}, err
	}
	if req.Version < 0 {
		return SetSummaryResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	if err := maxChars("summary", req.Summary, maxSummaryChars); err != nil {
		return SetSummaryResp{}, err
	}
	version := req.Version
	return mutate(ctx, s.c, caller, mutation{
		Tool: "set_summary", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a,
	}, func(t *txn) (SetSummaryResp, error) {
		sum, err := t.Tx.SetSummary(ctx, req.ProjectID, req.TaskID, req.Summary, caller.Identity, t.Task.Version)
		if err != nil {
			return SetSummaryResp{}, err
		}
		after, err := t.Bump()
		if err != nil {
			return SetSummaryResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventSummarySet, Data: json.RawMessage(`{}`)})
		out, err := taskOut(after, sum.Body)
		if err != nil {
			return SetSummaryResp{}, err
		}
		return SetSummaryResp{Task: out}, nil
	})
}

// GetTaskContext is get_task_context: the briefing of one task. The default call costs nine
// statements (the get_task_context pin): the task, its summary, a page of its items, its memory
// window, the edges on each side of it, its artifacts, its attachments and its open follow-ups, one
// statement per area and never one per row; an excluded section (include) issues none, and
// artifacts, not a section, are always read. The lanes come from the board snapshot (no statement
// when warm, store.GetBoard's two when cold), the membership from the access cache. upstream and
// projectMemory are opt-in and outside the pin: one ListTasksByIDs and a GetSummary per upstream
// task (at most limit), and one or two RecallMemory.
func (s contextSvc) GetTaskContext(ctx context.Context, caller domain.Caller, req GetTaskContextReq) (GetTaskContextResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "get_task_context", domain.GrantSubject{}, true); err != nil {
		return GetTaskContextResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return GetTaskContextResp{}, err
	}
	limit := int(req.Limit)
	switch {
	case req.Limit == 0:
		limit = contextDefaultLimit
	case req.Limit < 0 || req.Limit > contextMaxLimit:
		return GetTaskContextResp{}, domain.Validation("limit is 1 to 100", map[string]any{"field": "limit"})
	}
	include := map[ContextSection]bool{}
	for _, sec := range req.Include {
		if !slices.Contains(ContextSectionValues, sec) {
			return GetTaskContextResp{}, domain.Validation("unknown include section: "+string(sec), map[string]any{"field": "include"})
		}
		include[sec] = true
	}
	if req.Include == nil {
		for _, sec := range ContextSectionValues {
			include[sec] = true
		}
	}
	out, err := s.compose(ctx, caller, req, include, limit)
	if err != nil {
		return GetTaskContextResp{}, s.c.mapErr("get_task_context", err)
	}
	return out, nil
}

func (s contextSvc) compose(ctx context.Context, caller domain.Caller, req GetTaskContextReq, include map[ContextSection]bool, limit int) (GetTaskContextResp, error) {
	st := s.c.store
	task, err := st.GetTask(ctx, req.ProjectID, req.TaskID)
	if err != nil {
		return GetTaskContextResp{}, err
	}
	snap, err := s.c.boardSnapshot(ctx, req.ProjectID, task.BoardID)
	if err != nil {
		return GetTaskContextResp{}, err
	}
	out := GetTaskContextResp{
		Artifacts: []Artifact{}, Attachments: []AttachmentBrief{}, Fields: map[string]FieldValue{},
		Items: []ContextItem{}, Memory: []MemoryEntry{}, OpenFollowUps: []OpenFollowUp{},
		Deps:        TaskDeps{BlockedBy: []string{}, Blocks: []string{}, Edges: []DependencyEdge{}},
		Workflow:    Workflow{Status: task.Status, AllowedMoves: domain.AllowedNext(task.Status), Lanes: snap.Board.Columns},
		Deliverable: task.Deliverable,
	}
	if out.Workflow.AllowedMoves == nil {
		out.Workflow.AllowedMoves = []domain.Status{}
	}
	if out.Review, err = reviewOut(task.Review); err != nil {
		return GetTaskContextResp{}, err
	}
	if out.ModelHint, err = modelHintOut(task.ModelHint); err != nil {
		return GetTaskContextResp{}, err
	}
	arts, err := st.ListArtifacts(ctx, req.ProjectID, req.TaskID)
	if err != nil {
		return GetTaskContextResp{}, err
	}
	for _, a := range arts {
		out.Artifacts = append(out.Artifacts, artifactOut(a))
	}
	if include["attachments"] {
		atts, err := st.ListAttachments(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return GetTaskContextResp{}, err
		}
		for _, a := range atts {
			out.Attachments = append(out.Attachments, attachmentBriefOut(a))
		}
	}
	if include["followups"] {
		page, err := st.ListFollowUps(ctx, req.ProjectID, req.TaskID, store.FollowUpsQuery{OpenOnly: true, Limit: contextFollowUpsMax})
		if err != nil {
			return GetTaskContextResp{}, err
		}
		for _, f := range page.Items {
			out.OpenFollowUps = append(out.OpenFollowUps, followUpOut(f))
		}
		out.OpenFollowUpCount = float64(len(out.OpenFollowUps))
	}
	if include["summary"] {
		sum, err := st.GetSummary(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return GetTaskContextResp{}, err
		}
		out.Summary = sum.Body
	}
	if include["fields"] {
		if out.Fields, err = fieldsOut(task.CustomFields); err != nil {
			return GetTaskContextResp{}, err
		}
	}
	// resume (P-117) reuses the artifacts above and, when it is the newest page and settles the last
	// checkpoint, the items page below; otherwise it reads the newest resumeItemsMax items.
	var cp *Checkpoint
	settled := false
	if include["items"] {
		page, err := st.ListContextItems(ctx, req.ProjectID, req.TaskID, store.ContextItemsQuery{Limit: limit, Cursor: req.Cursor})
		if err != nil {
			return GetTaskContextResp{}, err
		}
		for _, it := range page.Items {
			out.Items = append(out.Items, itemOut(it))
		}
		out.NextCursor = page.NextCursor
		if req.Cursor == "" {
			cp = lastCheckpoint(page.Items)
			settled = cp != nil || page.NextCursor == ""
		}
	}
	if !settled {
		page, err := st.ListContextItems(ctx, req.ProjectID, req.TaskID, store.ContextItemsQuery{Limit: resumeItemsMax})
		if err != nil {
			return GetTaskContextResp{}, err
		}
		cp = lastCheckpoint(page.Items)
	}
	out.Resume = resumeFrom(arts, cp)
	if include["memory"] {
		entries, err := st.ReadMemory(ctx, req.ProjectID, req.TaskID, store.ReadMemoryQuery{Identity: caller.Identity, Limit: limit})
		if err != nil {
			return GetTaskContextResp{}, err
		}
		if out.Memory, err = memoryOut(entries); err != nil {
			return GetTaskContextResp{}, err
		}
	}
	var blockers []store.DependencyEdge
	if include["deps"] || req.IncludeUpstream {
		if blockers, err = st.ListBlockers(ctx, req.ProjectID, req.TaskID); err != nil {
			return GetTaskContextResp{}, err
		}
	}
	if include["deps"] {
		dependents, err := st.ListDependents(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return GetTaskContextResp{}, err
		}
		edges := append(slices.Clone(blockers), dependents...)
		slices.SortStableFunc(edges, func(a, b store.DependencyEdge) int {
			return cmp.Or(cmp.Compare(a.TS, b.TS), strings.Compare(a.ID, b.ID))
		})
		out.Deps = TaskDeps{BlockedBy: nonNil(task.BlockedBy), Blocks: nonNil(task.Blocks), Edges: make([]DependencyEdge, 0, len(edges))}
		for _, e := range edges {
			out.Deps.Edges = append(out.Deps.Edges, edgeOut(e))
		}
	}
	if req.IncludeUpstream {
		if out.Upstream, err = s.upstream(ctx, req.ProjectID, blockers, limit); err != nil {
			return GetTaskContextResp{}, err
		}
	}
	if req.IncludeProjectMemory {
		entries, err := s.c.recallDurable(ctx, caller.Identity, req.ProjectID, "", "", limit)
		if err != nil {
			return GetTaskContextResp{}, err
		}
		if out.ProjectMemory, err = memoryOut(entries); err != nil {
			return GetTaskContextResp{}, err
		}
	}
	return out, nil
}

// upstream is the blocks-edge blockers of the task (the edge stays after the blocker is DONE, so
// its deliverable is reachable once it has finished), at most limit, with their summaries.
func (s contextSvc) upstream(ctx context.Context, projectID string, blockers []store.DependencyEdge, limit int) ([]UpstreamTask, error) {
	var ids []string
	for _, e := range blockers {
		if e.Type == store.DependencyBlocks && !slices.Contains(ids, e.FromTaskID) {
			ids = append(ids, e.FromTaskID)
		}
	}
	ids = ids[:min(len(ids), limit)]
	out := []UpstreamTask{}
	if len(ids) == 0 {
		return out, nil
	}
	tasks, err := s.c.store.ListTasksByIDs(ctx, projectID, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		sum, err := s.c.store.GetSummary(ctx, projectID, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, UpstreamTask{TaskID: t.ID, Title: t.Title, Status: string(t.Status), Deliverable: t.Deliverable, Summary: sum.Body})
	}
	return out, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func isNullJSON(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func decodeJSON[T any](field string, raw json.RawMessage) (*T, error) {
	if isNullJSON(raw) {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, internalf("task %s is not valid JSON: %v", field, err)
	}
	return &v, nil
}

func fieldsOut(raw json.RawMessage) (map[string]FieldValue, error) {
	m, err := decodeJSON[map[string]FieldValue]("customFields", raw)
	if err != nil || m == nil || *m == nil {
		return map[string]FieldValue{}, err
	}
	return *m, nil
}

func reviewOut(raw json.RawMessage) (*TaskReview, error) {
	return decodeJSON[TaskReview]("review", raw)
}

func modelHintOut(raw json.RawMessage) (*ModelHint, error) {
	return decodeJSON[ModelHint]("modelHint", raw)
}

func isoPtr(ms *int64) *string {
	if ms == nil {
		return nil
	}
	s := isoMs(*ms)
	return &s
}

// taskOut is a store task as the wire carries it. summary is the task's contextSummary, which lives
// in its own table (store.Summaries). Artifacts are in theirs (store.Artifacts) and are left out
// here; withArtifacts adds them where the answer carries them.
func taskOut(t store.Task, summary string) (Task, error) {
	out := Task{
		Assignee: t.Assignee, BlockedBy: nonNil(t.BlockedBy), Blocks: nonNil(t.Blocks), BoardID: t.BoardID,
		ColumnID: t.ColumnID, ContextSummary: summary, CreatedAt: isoMs(t.CreatedAt), CreatedBy: t.CreatedBy,
		Description: t.Description, DueAt: isoPtr(t.DueAt), Estimate: t.Estimate, ID: t.ID, Labels: nonNil(t.Labels),
		Order: t.OrderKey, ParentTaskID: t.ParentTaskID, Priority: t.Priority, ProjectID: t.ProjectID,
		RequiredCapabilities: nonNil(t.RequiredCapabilities), Status: t.Status, Title: t.Title,
		UpdatedAt: isoMs(t.UpdatedAt), UpdatedBy: t.UpdatedBy, Version: float64(t.Version),
		ClaimAttempts: float64(t.ClaimAttempts), CreatedByAgent: t.CreatedByAgent, Deliverable: t.Deliverable,
		NextEligibleAt: isoPtr(t.NextEligibleAt), RaceOpenedAt: isoPtr(t.RaceOpenedAt), Racing: t.Racing,
		Reviewer: t.Reviewer, RunOf: t.RunOf, SprintIDs: t.SprintIDs,
	}
	if t.Claim != nil {
		out.Claim = &TaskClaim{ExpiresAt: isoMs(t.Claim.ExpiresAt), Owner: t.Claim.Owner, Agent: t.Claim.Agent, AgentID: t.Claim.AgentID}
	}
	var err error
	if out.CustomFields, err = fieldsOut(t.CustomFields); err != nil {
		return Task{}, err
	}
	if out.Review, err = reviewOut(t.Review); err != nil {
		return Task{}, err
	}
	if out.ModelHint, err = modelHintOut(t.ModelHint); err != nil {
		return Task{}, err
	}
	chars, err := decodeJSON[ContextChars]("chars", t.Chars)
	if err != nil {
		return Task{}, err
	}
	if chars != nil {
		out.Chars = *chars
	}
	return out, nil
}

// attachmentBriefOut is an attachment as get_task_context carries it: list_attachments' row
// (media.go) without a download URL, so the briefing signs nothing. gcsUri is "blob:" and the
// upload ref; sha256 is set once the attachment is ready; thumbUri and extractedText are null (no
// media function).
func attachmentBriefOut(a store.Attachment) AttachmentBrief {
	out := AttachmentBrief{
		Bytes: float64(a.Bytes), CreatedBy: a.CreatedBy, GCSURI: "blob:" + blob.Ref{ProjectID: a.ProjectID, TaskID: a.TaskID, AttachmentID: a.ID}.String(),
		ID: a.ID, Kind: MediaKind(a.Kind), MimeType: a.MimeType, Status: AttachmentStatus(a.Status), TaskID: a.TaskID, Ts: isoMs(a.TS),
	}
	if a.Status == store.AttachmentReady {
		sha := a.BlobSHA256
		out.SHA256 = &sha
	}
	return out
}

func itemOut(it store.ContextItem) ContextItem {
	return ContextItem{Author: it.Author, Body: it.Body, ID: it.ID, Kind: ContextKind(it.Kind), TaskID: it.TaskID, Ts: isoMs(it.TS)}
}

func edgeOut(e store.DependencyEdge) DependencyEdge {
	return DependencyEdge{CreatedBy: e.CreatedBy, FromTaskID: e.FromTaskID, ID: e.ID, ProjectID: e.ProjectID, ToTaskID: e.ToTaskID, Ts: isoMs(e.TS), Type: DependencyType(e.Type)}
}
