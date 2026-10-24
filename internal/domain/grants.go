package domain

import (
	"slices"
	"sort"
	"strings"
)

// Priority is a task's priority.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

var priorityRank = map[Priority]int{PriorityLow: 0, PriorityNormal: 1, PriorityHigh: 2, PriorityUrgent: 3}

// MemoryScope is a memory item's scope (write_memory's scope argument).
type MemoryScope string

const (
	MemoryScopeTask        MemoryScope = "task"
	MemoryScopeAgent       MemoryScope = "agent"
	MemoryScopeShared      MemoryScope = "shared"
	MemoryScopeProject     MemoryScope = "project"
	MemoryScopeAgentGlobal MemoryScope = "agent_global"
)

// AdminOnly marks a tool that gates on workspace admin: on every branch ("only", hidden from a
// non-admin's tools/list) or on one branch ("conditional", listed and gated in the handler).
type AdminOnly string

const (
	AdminOnlyOnly        AdminOnly = "only"
	AdminOnlyConditional AdminOnly = "conditional"
)

// LandingFacts are the call facts a dynamic landing reads.
type LandingFacts struct {
	// ReviewEnabled is the project's setting, nil when the caller did not supply it.
	ReviewEnabled *bool
	ToStatus      Status
	ToColumnID    string
}

// Landing gives the statuses a tool can leave its task in; ok false means it cannot be decided from
// the call, which a statuses grant refuses.
type Landing func(LandingFacts) (statuses []Status, ok bool)

func fixedLanding(s ...Status) Landing {
	return func(LandingFacts) ([]Status, bool) { return s, true }
}

// completionLanding: where a completion lands is the project's reviewEnabled, never an argument.
func completionLanding(f LandingFacts) ([]Status, bool) {
	if f.ReviewEnabled == nil {
		return nil, false
	}
	if *f.ReviewEnabled {
		return []Status{Review}, true
	}
	return []Status{Done}, true
}

// moveLanding: move_task names its status only in the toStatus form; a toColumnId-only move would need
// the board to resolve, so it is undecidable here and a statuses grant refuses it.
func moveLanding(f LandingFacts) ([]Status, bool) {
	switch {
	case f.ToStatus != "":
		return []Status{f.ToStatus}, true
	case f.ToColumnID != "":
		return nil, false
	}
	return []Status{}, true
}

// Posture is a tool's authorization posture.
type Posture struct {
	// Tier is the rank the tool needs; "" for the lease-guarded outcomes (the held lease is the
	// guard), cancel_task (the master role or the task's owner) and the self- or workspace-scoped tools.
	Tier Tier
	// TenantScoped: the tool takes a required projectId.
	TenantScoped bool
	AdminOnly    AdminOnly
	// GrantExempt: a capability grant never narrows the tool (the reads and the self-scoped tools).
	// Inverted on purpose: leaving it false governs the tool, so a new tool fails closed.
	GrantExempt bool
	// Landing is where a governed tool leaves its task; required on every governed tool, absent on
	// every exempt one.
	Landing Landing
	// Effect is what the tool does to stored data; MCP clients read it as tool annotations.
	Effect Effect
	// Idempotent: a repeat with the same arguments changes nothing more (a set, a removal, or a
	// version-checked write the repeat finds STALE). Every read is idempotent whatever this says.
	Idempotent bool
}

// Effect classifies a tool for the MCP annotations readOnlyHint and destructiveHint. The zero
// value is unclassified, so a row without one fails grants_test.go.
type Effect int

const (
	_ Effect = iota // unclassified
	// EffectRead changes nothing.
	EffectRead
	// EffectWrite adds, or changes what the event log records; it never deletes.
	EffectWrite
	// EffectDestructive deletes rows, tombstones one, or revokes access or a credential.
	EffectDestructive
)

// toolPosture is the one place every tool is classified: the 86 tools of the target surface
// (conductor's 91 in scope, delete_task and cancel_task, less ADR-0004's 7). The exempt set, the
// unscoped set and the landing statuses all derive from it.
var toolPosture = map[string]Posture{
	"ack_follow_up":             {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"add_artifact":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"add_column":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"add_comment":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"add_context_item":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"add_dependency":            {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"add_follow_up":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"add_member":                {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"add_subtask":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"attach_file":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"block_task":                {TenantScoped: true, Landing: fixedLanding(Blocked), Effect: EffectWrite},
	"cancel_runs":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectDestructive, Idempotent: true},
	"cancel_task":               {TenantScoped: true, Landing: fixedLanding(Done), Effect: EffectWrite, Idempotent: true},
	"claim_next":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(InProgress), Effect: EffectWrite},
	"claim_task":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(InProgress), Effect: EffectWrite},
	"complete_task":             {TenantScoped: true, Landing: completionLanding, Effect: EffectWrite},
	"create_board":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"create_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"create_integration":        {Tier: TierStructural, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectWrite},
	"create_project":            {TenantScoped: false, GrantExempt: true, Effect: EffectWrite},
	"create_sprint":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"create_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"delete_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"delete_integration":        {Tier: TierStructural, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"delete_project":            {Tier: TierAdmin, TenantScoped: true, AdminOnly: AdminOnlyConditional, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"delete_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"detect_anomalies":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectRead},
	"disconnect_agent":          {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"edit_chronicle_page":       {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"fail_task":                 {TenantScoped: true, Landing: fixedLanding(Todo, Blocked), Effect: EffectWrite},
	"get_board":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"get_my_settings":           {TenantScoped: false, GrantExempt: true, Effect: EffectRead},
	"get_project":               {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"get_skill_stats":           {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"get_sprint":                {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"get_task":                  {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"get_task_context":          {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"grant_capabilities":        {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"handoff_task":              {TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"heartbeat_task":            {TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"ingest_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"list_agent_sessions":       {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding(), Effect: EffectRead},
	"list_attachments":          {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_audit_log":            {TenantScoped: false, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectRead},
	"list_boards":               {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_comments":             {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_deliveries":           {Tier: TierMaster, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectRead},
	"list_follow_ups":           {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_integrations":         {Tier: TierMaster, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectRead},
	"list_notifications":        {TenantScoped: false, GrantExempt: true, Effect: EffectRead},
	"list_projects":             {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding(), Effect: EffectRead},
	"list_runs":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_sprints":              {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_subtasks":             {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"list_tasks":                {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"mark_notifications_read":   {TenantScoped: false, GrantExempt: true, Effect: EffectWrite, Idempotent: true},
	"move_task":                 {Tier: TierMaster, TenantScoped: true, Landing: moveLanding, Effect: EffectWrite, Idempotent: true},
	"next_task":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"open_run":                  {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
	"orchestrate_subtasks":      {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Blocked), Effect: EffectWrite},
	"pick_run":                  {Tier: TierMaster, TenantScoped: true, Landing: completionLanding, Effect: EffectDestructive, Idempotent: true},
	"rank_tasks":                {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"read_delivery_raw":         {Tier: TierMaster, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectRead},
	"read_memory":               {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"reap_stale_leases":         {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(Todo, Blocked), Effect: EffectWrite},
	"recall_memory":             {TenantScoped: false, GrantExempt: true, Effect: EffectRead},
	"register_agent_profile":    {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"release_task":              {TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"remove_column":             {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"remove_dependency":         {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"remove_member":             {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"replay_delivery":           {Tier: TierMaster, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"retract_artifact":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"rotate_integration_secret": {Tier: TierStructural, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectDestructive, Idempotent: true},
	"search_tasks":              {Tier: TierRead, TenantScoped: true, GrantExempt: true, Effect: EffectRead},
	"send_test_event":           {Tier: TierMaster, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(Todo), Effect: EffectWrite},
	"set_my_settings":           {TenantScoped: false, GrantExempt: true, Effect: EffectWrite, Idempotent: true},
	"set_sprint":                {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"set_summary":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"start_runs":                {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo), Effect: EffectWrite, Idempotent: true},
	"update_column":             {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"update_integration":        {Tier: TierStructural, TenantScoped: true, AdminOnly: AdminOnlyOnly, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"update_settings":           {Tier: TierAdmin, TenantScoped: true, AdminOnly: AdminOnlyConditional, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"update_sprint":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"update_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite, Idempotent: true},
	"write_memory":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(), Effect: EffectWrite},
}

// ToolPosture returns a tool's posture; ok is false for a tool nobody classified.
func ToolPosture(tool string) (Posture, bool) {
	p, ok := toolPosture[tool]
	return p, ok
}

// ClassifiedTools returns every classified tool name, sorted.
func ClassifiedTools() []string {
	return sortedTools(func(string, Posture) bool { return true })
}

// GrantExemptTools are the tools a capability grant never narrows.
func GrantExemptTools() []string {
	return sortedTools(func(_ string, p Posture) bool { return p.GrantExempt })
}

// GrantUnscopedTools are governed but take no projectId, and a grant is stored on a project, so the
// grant stage never reaches them: a grant naming one stores and does nothing.
func GrantUnscopedTools() []string {
	return sortedTools(func(_ string, p Posture) bool { return !p.TenantScoped && !p.GrantExempt })
}

// IsGrantGoverned reports whether a grant can narrow the tool: anything not exempt is governed,
// including a tool nobody classified.
func IsGrantGoverned(tool string) bool {
	return !toolPosture[tool].GrantExempt
}

func sortedTools(keep func(string, Posture) bool) []string {
	var out []string
	for name, p := range toolPosture {
		if keep(name, p) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// CapabilityGrant narrows what one member may do within their role. Every present field is a
// restriction, so the zero grant restricts nothing. For the slices, nil is absent and an empty slice
// is present (it admits nothing); omitzero keeps that distinction through JSON.
type CapabilityGrant struct {
	// Tools is an allowlist: when present, any other governed tool is refused.
	Tools []string `json:"tools,omitzero"`
	// Deny wins over Tools.
	Deny []string `json:"deny,omitzero"`
	// Labels: the task must carry at least one.
	Labels []string `json:"labels,omitzero"`
	// Boards: the task's board, or a boardId argument, must be one of these.
	Boards []string `json:"boards,omitzero"`
	// MaxPriority caps the task's priority; "" is absent.
	MaxPriority Priority `json:"maxPriority,omitzero"`
	// Statuses: every status the calling tool can land the task in must be one of these.
	Statuses []Status `json:"statuses,omitzero"`
	// MemoryScopes: the allowed values of a scope argument (write_memory).
	MemoryScopes []MemoryScope `json:"memoryScopes,omitzero"`
}

// GrantTask is the part of the target task a grant reads.
type GrantTask struct {
	BoardID  string
	Labels   []string
	Priority Priority
}

// GrantSubject is what a call asks to do, assembled from its arguments and, when GrantNeedsTask, the
// task. "" and nil are absent.
type GrantSubject struct {
	Tool          string
	BoardID       string
	ToStatus      Status
	ToColumnID    string
	ReviewEnabled *bool
	MemoryScope   MemoryScope
	// Labels is a labels argument (create_task, update_task), used when there is no task yet.
	Labels []string
	Task   *GrantTask
}

// GrantNeedsTask reports whether deciding g needs the target task: only the task-attribute
// restrictions do, so a grant that scopes tool names costs no read.
func GrantNeedsTask(g CapabilityGrant) bool {
	return g.Labels != nil || g.Boards != nil || g.MaxPriority != ""
}

// GrantRefusesTool reports whether g refuses tool by its name alone: deny lists it (deny wins), or a
// tools allowlist leaves it out.
func GrantRefusesTool(g CapabilityGrant, tool string) bool {
	return slices.Contains(g.Deny, tool) || (g.Tools != nil && !slices.Contains(g.Tools, tool))
}

// GrantHiddenTools is what tools/list leaves out for an identity, given one grant per membership (the
// zero grant for a membership a grant does not bind): the governed project tools every grant refuses
// by name. A tool any membership may call stays listed, and the task, status and memory restrictions
// are decided per call, so they hide nothing. No membership hides nothing.
func GrantHiddenTools(grants []CapabilityGrant) []string {
	if len(grants) == 0 {
		return nil
	}
	var out []string
	for _, name := range sortedTools(func(_ string, p Posture) bool { return p.TenantScoped && !p.GrantExempt }) {
		if !slices.ContainsFunc(grants, func(g CapabilityGrant) bool { return !GrantRefusesTool(g, name) }) {
			out = append(out, name)
		}
	}
	return out
}

// CheckGrantEnforceable refuses a grant whose deny or tools names a tool no grant reaches
// (GrantUnscopedTools): a grant is stored on a project and those tools take no projectId, so the
// entry would be stored and never enforced. Their reach is the role's.
func CheckGrantEnforceable(g CapabilityGrant) error {
	unscoped := GrantUnscopedTools()
	for _, f := range []struct {
		field string
		tools []string
	}{{"deny", g.Deny}, {"tools", g.Tools}} {
		var named []string
		for _, t := range f.tools {
			if slices.Contains(unscoped, t) {
				named = append(named, t)
			}
		}
		if len(named) > 0 {
			return Validation(f.field+" names "+strings.Join(named, ", ")+", which a grant never restricts: a grant is stored on a project and "+
				"these tools take no projectId, so the entry would never be enforced; their reach is governed by role",
				map[string]any{"field": f.field, "tools": named})
		}
	}
	return nil
}

// CheckGrant decides one call against one grant: "" when permitted, else the capability the caller
// lacks (details.requiredCapability on the FORBIDDEN). What it cannot resolve it refuses: an
// unclassified or undecidable landing under a statuses grant, a task-scoped grant on a call that
// names neither a task nor a board.
func CheckGrant(g CapabilityGrant, s GrantSubject) string {
	if GrantRefusesTool(g, s.Tool) {
		return "tool:" + s.Tool
	}
	if g.Statuses != nil {
		p, classified := toolPosture[s.Tool]
		if !classified || p.Landing == nil {
			return "status:*"
		}
		landing, ok := p.Landing(LandingFacts{ReviewEnabled: s.ReviewEnabled, ToStatus: s.ToStatus, ToColumnID: s.ToColumnID})
		if !ok {
			return "status:*"
		}
		for _, st := range landing {
			if !slices.Contains(g.Statuses, st) {
				return "status:" + string(st)
			}
		}
	}
	if g.MemoryScopes != nil && s.MemoryScope != "" && !slices.Contains(g.MemoryScopes, s.MemoryScope) {
		return "memory-scope:" + string(s.MemoryScope)
	}
	if g.Boards != nil {
		board := s.BoardID
		if s.Task != nil {
			board = s.Task.BoardID
		}
		if board == "" || !slices.Contains(g.Boards, board) {
			if board == "" {
				board = "*"
			}
			return "board:" + board
		}
	}
	if g.Labels != nil {
		labels := s.Labels
		if s.Task != nil {
			labels = s.Task.Labels
		}
		if !slices.ContainsFunc(labels, func(l string) bool { return slices.Contains(g.Labels, l) }) {
			want := strings.Join(g.Labels, "|")
			if want == "" {
				want = "*"
			}
			return "label:" + want
		}
	}
	if g.MaxPriority != "" {
		if s.Task == nil {
			return "priority<=" + string(g.MaxPriority)
		}
		rank, known := priorityRank[s.Task.Priority]
		if !known || rank > priorityRank[g.MaxPriority] {
			return "priority<=" + string(g.MaxPriority)
		}
	}
	return ""
}

// WorkerDefaultGrant is the least-privilege grant for a new worker: pull work, do it, report on it,
// annotate it. It leaves out fail_task and block_task (outcome judgements), add_comment, attach_file
// and set_summary (broadcast surface) and reap_stale_leases (other agents' live work), all of which
// the worker role still permits when no grant is attached. Its statuses stop at REVIEW, so on a board
// with review off the grant refuses complete_task: it withholds approving one's own work.
func WorkerDefaultGrant() CapabilityGrant {
	return CapabilityGrant{
		Tools: []string{
			"claim_task", "claim_next", "heartbeat_task", "release_task", "complete_task",
			"add_context_item", "add_artifact", "retract_artifact", "write_memory",
		},
		Statuses:     []Status{Todo, InProgress, Review},
		MemoryScopes: []MemoryScope{MemoryScopeTask},
	}
}
