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
}

// toolPosture is the one place every tool is classified: the 93 tools of the target surface
// (conductor's 91 in scope, delete_task and cancel_task). The exempt set, the unscoped set and the
// landing statuses all derive from it.
var toolPosture = map[string]Posture{
	"ack_follow_up":             {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"add_artifact":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"add_column":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"add_comment":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"add_context_item":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"add_dependency":            {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"add_follow_up":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo)},
	"add_member":                {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding()},
	"add_subtask":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"attach_file":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"block_task":                {TenantScoped: true, Landing: fixedLanding(Blocked)},
	"cancel_runs":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo)},
	"cancel_task":               {TenantScoped: true, Landing: fixedLanding(Done)},
	"claim_next":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(InProgress)},
	"claim_task":                {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(InProgress)},
	"complete_task":             {TenantScoped: true, Landing: completionLanding},
	"create_board":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"create_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"create_cli_token":          {TenantScoped: false, GrantExempt: true},
	"create_integration":        {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"create_project":            {TenantScoped: false, GrantExempt: true},
	"create_sprint":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"create_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"delete_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"delete_integration":        {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"delete_project":            {Tier: TierAdmin, TenantScoped: true, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"delete_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"detect_anomalies":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"disconnect_agent":          {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"edit_chronicle_page":       {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"fail_task":                 {TenantScoped: true, Landing: fixedLanding(Todo, Blocked)},
	"find_tools":                {TenantScoped: false, GrantExempt: true},
	"get_board":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_my_settings":           {TenantScoped: false, GrantExempt: true},
	"get_project":               {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_skill_stats":           {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_sprint":                {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_task":                  {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_task_context":          {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"get_tool_schema":           {TenantScoped: false, GrantExempt: true},
	"grant_capabilities":        {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding()},
	"handoff_task":              {TenantScoped: true, Landing: fixedLanding(Todo)},
	"heartbeat_task":            {TenantScoped: true, Landing: fixedLanding()},
	"ingest_chronicle_page":     {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"invoke_tool":               {TenantScoped: false, GrantExempt: true},
	"list_agent_sessions":       {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"list_attachments":          {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_audit_log":            {TenantScoped: false, AdminOnly: AdminOnlyOnly, Landing: fixedLanding()},
	"list_boards":               {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_cli_tokens":           {TenantScoped: false, GrantExempt: true},
	"list_comments":             {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_deliveries":           {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"list_follow_ups":           {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_integrations":         {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"list_notifications":        {TenantScoped: false, GrantExempt: true},
	"list_projects":             {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"list_runs":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_sprints":              {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_subtasks":             {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"list_tasks":                {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"mark_notifications_read":   {TenantScoped: false, GrantExempt: true},
	"move_task":                 {Tier: TierMaster, TenantScoped: true, Landing: moveLanding},
	"next_task":                 {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"open_run":                  {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"orchestrate_subtasks":      {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Blocked)},
	"pick_run":                  {Tier: TierMaster, TenantScoped: true, Landing: completionLanding},
	"prune_agent_sessions":      {TenantScoped: false, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"rank_tasks":                {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"read_delivery_raw":         {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"read_memory":               {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"reap_stale_leases":         {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding(Todo, Blocked)},
	"recall_memory":             {TenantScoped: false, GrantExempt: true},
	"register_agent_profile":    {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"release_task":              {TenantScoped: true, Landing: fixedLanding(Todo)},
	"remove_column":             {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"remove_dependency":         {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"remove_member":             {Tier: TierAdmin, TenantScoped: true, Landing: fixedLanding()},
	"replay_delivery":           {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo)},
	"retract_artifact":          {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"revoke_cli_token":          {TenantScoped: false, GrantExempt: true},
	"rotate_integration_secret": {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"search_tasks":              {Tier: TierRead, TenantScoped: true, GrantExempt: true},
	"send_test_event":           {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo)},
	"set_my_settings":           {TenantScoped: false, GrantExempt: true},
	"set_sprint":                {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"set_summary":               {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
	"start_runs":                {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding(Todo)},
	"update_column":             {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"update_integration":        {Tier: TierStructural, TenantScoped: true, Landing: fixedLanding()},
	"update_settings":           {Tier: TierAdmin, TenantScoped: true, AdminOnly: AdminOnlyConditional, Landing: fixedLanding()},
	"update_sprint":             {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"update_task":               {Tier: TierMaster, TenantScoped: true, Landing: fixedLanding()},
	"write_memory":              {Tier: TierWrite, TenantScoped: true, Landing: fixedLanding()},
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

// CheckGrant decides one call against one grant: "" when permitted, else the capability the caller
// lacks (details.requiredCapability on the FORBIDDEN). What it cannot resolve it refuses: an
// unclassified or undecidable landing under a statuses grant, a task-scoped grant on a call that
// names neither a task nor a board.
func CheckGrant(g CapabilityGrant, s GrantSubject) string {
	if slices.Contains(g.Deny, s.Tool) {
		return "tool:" + s.Tool
	}
	if g.Tools != nil && !slices.Contains(g.Tools, s.Tool) {
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
