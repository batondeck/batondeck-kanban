package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Profiles is the agent capability registry and its skill stats (conductor agents.ts, T-3): an
// agent's durable profile per project, keyed by its agent name (the caller's identity when it sends
// none), and the outcome rollups complete_task and block_task write.
type Profiles interface {
	// Register is register_agent_profile.
	// Tier: write. Creates or overwrites the caller's profile in place (an omitted field resets); the
	// first registration's creator and time are kept. No event, no audit, no change row: a profile is
	// not board state.
	Register(ctx context.Context, caller domain.Caller, req RegisterAgentProfileReq) (RegisterAgentProfileResp, error)

	// SkillStats is get_skill_stats.
	// Tier: read. Writes nothing. The agent's profile (null when unregistered) and its stats, each
	// with its regularised reliability and mean handle time.
	SkillStats(ctx context.Context, caller domain.Caller, req GetSkillStatsReq) (GetSkillStatsResp, error)
}

// CostClass is a profile's cost class.
type CostClass string

// CostClassValues are CostClass's values, in the schema's order.
var CostClassValues = []CostClass{"low", "premium", "standard"}

// RegisterAgentProfileReq is register_agent_profile's input.
type RegisterAgentProfileReq struct {
	ProjectID      string    `json:"projectId"`
	Capabilities   []string  `json:"capabilities"`
	CostClass      CostClass `json:"costClass,omitzero"`
	MaxConcurrency int64     `json:"maxConcurrency,omitzero"`
	MaxEffort      Effort    `json:"maxEffort,omitzero"`
	Models         []string  `json:"models,omitzero"`
	Tags           []string  `json:"tags,omitzero"`
}

// RegisterAgentProfileResp is register_agent_profile's output.
type RegisterAgentProfileResp struct {
	Profile AgentProfile `json:"profile"`
}

// AgentProfile is register_agent_profile's output profile.
type AgentProfile struct {
	AgentKey       string     `json:"agentKey"`
	Capabilities   []string   `json:"capabilities"`
	CostClass      *CostClass `json:"costClass"`
	CreatedAt      string     `json:"createdAt"`
	CreatedBy      string     `json:"createdBy"`
	MaxConcurrency *float64   `json:"maxConcurrency"`
	MaxEffort      *Effort    `json:"maxEffort"`
	Models         []string   `json:"models"`
	ProjectID      string     `json:"projectId"`
	Tags           []string   `json:"tags"`
	UpdatedAt      string     `json:"updatedAt"`
}

// GetSkillStatsReq is get_skill_stats's input.
type GetSkillStatsReq struct {
	ProjectID string `json:"projectId"`
	AgentKey  string `json:"agentKey,omitzero"`
}

// GetSkillStatsResp is get_skill_stats's output.
type GetSkillStatsResp struct {
	Profile *AgentProfile `json:"profile"`
	Stats   []SkillStat   `json:"stats"`
}

// SkillStat is get_skill_stats's output stats[].
type SkillStat struct {
	AgentKey    string   `json:"agentKey"`
	Blocked     float64  `json:"blocked"`
	Capability  string   `json:"capability"`
	Completed   float64  `json:"completed"`
	MeanMs      *float64 `json:"meanMs"`
	ProjectID   string   `json:"projectId"`
	Reliability float64  `json:"reliability"`
	Samples     float64  `json:"samples"`
	TotalMs     float64  `json:"totalMs"`
	UpdatedAt   string   `json:"updatedAt"`
}
