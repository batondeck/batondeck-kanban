package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// AgentProfile is an agent's durable capability profile in a project (conductor T-3), keyed by its
// agent name. MaxConcurrency, CostClass and MaxEffort are nil when unset; times are Unix
// milliseconds.
type AgentProfile struct {
	ProjectID      string
	AgentKey       string
	Capabilities   []string
	Tags           []string
	MaxConcurrency *int64
	CostClass      *string
	Models         []string
	MaxEffort      *string
	CreatedBy      string
	CreatedAt      int64
	UpdatedAt      int64
}

// UpsertAgentProfileInput is a profile to create or replace; Now stamps it.
type UpsertAgentProfileInput struct {
	ProjectID      string
	AgentKey       string
	Capabilities   []string
	Tags           []string
	MaxConcurrency *int64
	CostClass      *string
	Models         []string
	MaxEffort      *string
	CreatedBy      string
	Now            int64
}

// SkillStat is one (agent, capability) outcome rollup.
type SkillStat struct {
	ProjectID  string
	AgentKey   string
	Capability string
	Completed  int64
	Blocked    int64
	TotalMs    int64
	Samples    int64
	UpdatedAt  int64
}

// SkillOutcome is one terminal outcome of a task to roll up, once per capability: completed, else
// blocked.
type SkillOutcome struct {
	ProjectID    string
	AgentKey     string
	Capabilities []string
	Completed    bool
	ElapsedMs    int64
	Now          int64
}

// AgentProfiles is the capability registry and skill stats area. Keys: a profile is (project_id,
// agent_key), a stat (project_id, agent_key, capability). Errors: a missing project is
// domain.NotFound("Project", id), a missing profile domain.NotFound("AgentProfile", key); an empty
// agent key or creator, a cost class or effort outside the schema's and Now <= 0 are
// domain.Validation. Both leave with their project, by its cascade.
type AgentProfiles interface {
	// UpsertAgentProfile creates the profile or replaces every field of it but CreatedBy and
	// CreatedAt, which the first registration set.
	UpsertAgentProfile(ctx context.Context, in UpsertAgentProfileInput) (AgentProfile, error)
	// GetAgentProfile is one profile.
	GetAgentProfile(ctx context.Context, projectID, agentKey string) (AgentProfile, error)
	// RecordSkillOutcome adds the outcome to each capability's rollup: one sample, its elapsed time
	// (negative is 0) and one completion or one block. No capability is a no-op. It is one
	// statement per capability.
	RecordSkillOutcome(ctx context.Context, in SkillOutcome) error
	// ListSkillStats is the agent's stats in the project, by capability ascending (as bytes), at
	// most 200.
	ListSkillStats(ctx context.Context, projectID, agentKey string) ([]SkillStat, error)
}

var (
	_ AgentProfiles = (*SQLite)(nil)
	_ AgentProfiles = (*Postgres)(nil)
)

// CheckAgentProfile is UpsertAgentProfile's validation, shared with memstore.
func CheckAgentProfile(in UpsertAgentProfileInput) error {
	switch {
	case in.AgentKey == "":
		return domain.Validation("a profile needs an agent key", nil)
	case in.CreatedBy == "":
		return domain.Validation("a profile needs a creator", nil)
	case in.Now <= 0:
		return domain.Validation("a profile needs a time", nil)
	}
	if in.CostClass != nil && *in.CostClass != "low" && *in.CostClass != "standard" && *in.CostClass != "premium" {
		return domain.Validation("unknown cost class: "+*in.CostClass, nil)
	}
	if in.MaxEffort != nil && *in.MaxEffort != "low" && *in.MaxEffort != "medium" && *in.MaxEffort != "high" && *in.MaxEffort != "xhigh" {
		return domain.Validation("unknown effort: "+*in.MaxEffort, nil)
	}
	return nil
}
