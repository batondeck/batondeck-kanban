package service

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Profiles is the capability registry service.
func (c *Core) Profiles() Profiles { return profilesSvc{c} }

type profilesSvc struct{ c *Core }

const (
	maxProfileCaps   = 100  // capabilities and tags: conductor's z.array(z.string()).max(100)
	maxProfileModels = 50   // models: .max(50), each 1 to 100 characters
	maxConcurrencyN  = 1000 // maxConcurrency: positive, at most 1000
)

// placeholder is an unexpanded template variable sent as an agent name (conductor's
// sanitizeAgentName): ${X}, $X, {{X}} or %X%.
var placeholder = regexp.MustCompile(`\$\{[^}]*\}|^\$[A-Za-z_][A-Za-z0-9_]*$|^\{\{.*\}\}$|^%.+%$`)

// agentKey is the name a caller's profile and stats are keyed by: its agent name, else its
// identity. An agent name that is an unexpanded placeholder is VALIDATION: a durable row must not be
// minted from it. The key is the agent name alone, so a name the client chose (an OAuth
// client_name, a clientInfo.name) can equal another identity's label: ownProfileKey refuses that
// key to register_agent_profile and rank_tasks {useProfile}, and rollUpSkill records such a
// caller's outcome under its identity instead (P-340; the label rule is labelRefused, R2-Q1). An
// agentless caller's key is its identity id, so a name equal to another identity's id would be that
// identity's agentless key: the same three guards treat it so (P-351, namesAnotherIdentity).
func agentKey(caller domain.Caller) (string, error) {
	name := strings.TrimSpace(caller.Agent)
	if name == "" {
		return caller.Identity, nil
	}
	if placeholder.MatchString(name) {
		return "", domain.Validation("Agent name is empty or an unexpanded placeholder: send a literal agent name before registering a profile",
			map[string]any{"agentKey": name})
	}
	return name, nil
}

// ownProfileKey is the caller's agentKey in projectID, FORBIDDEN when it is another agent's label
// for the caller by labelRefused, or another identity's id by namesAnotherIdentity (an account's,
// or a member's of the project: that identity's agentless key, P-351), with no master pass, as at a
// wake register: a profile is its agent's, and a label has no admin override (P-340; REVIEW-257
// R2-Q2). A caller with no agent (its key is its identity) reads nothing.
func (c *Core) ownProfileKey(ctx context.Context, caller domain.Caller, projectID, tool string) (string, error) {
	key, err := agentKey(caller)
	if err != nil || strings.TrimSpace(caller.Agent) == "" {
		return key, err
	}
	refused, err := c.labelRefused(ctx, caller, key)
	if err != nil {
		return "", c.mapErr(tool, err)
	}
	if refused {
		return "", forbidden("agentKey is another agent's label: a profile is written and used only on that label's own token",
			map[string]any{"field": "agentKey", "label": key})
	}
	named, err := namesAnotherIdentity(ctx, c.store, c.cachedMember, projectID, caller, key)
	if err != nil {
		return "", c.mapErr(tool, err)
	}
	if named {
		return "", forbidden("agentKey is another identity's id: that key is that identity's own agentless profile",
			map[string]any{"field": "agentKey", "agentKey": key})
	}
	return key, nil
}

// Register is register_agent_profile: one upsert.
func (s profilesSvc) Register(ctx context.Context, caller domain.Caller, req RegisterAgentProfileReq) (RegisterAgentProfileResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "register_agent_profile", domain.GrantSubject{}, false); err != nil {
		return RegisterAgentProfileResp{}, err
	}
	for field, list := range map[string][]string{"capabilities": req.Capabilities, "tags": req.Tags} {
		if len(list) > maxProfileCaps {
			return RegisterAgentProfileResp{}, domain.Validation(field+" has more than 100 entries", map[string]any{"field": field})
		}
	}
	if len(req.Models) > maxProfileModels {
		return RegisterAgentProfileResp{}, domain.Validation("models has more than 50 entries", map[string]any{"field": "models"})
	}
	for _, m := range req.Models {
		if m == "" || jsLen(m) > 100 {
			return RegisterAgentProfileResp{}, domain.Validation("models entries must be 1 to 100 characters", map[string]any{"field": "models"})
		}
	}
	if req.MaxConcurrency < 0 || req.MaxConcurrency > maxConcurrencyN {
		return RegisterAgentProfileResp{}, domain.Validation("maxConcurrency is 1 to 1000", map[string]any{"field": "maxConcurrency"})
	}
	if req.CostClass != "" && !slices.Contains(CostClassValues, req.CostClass) {
		return RegisterAgentProfileResp{}, domain.Validation("unknown costClass: "+string(req.CostClass), map[string]any{"field": "costClass"})
	}
	if req.MaxEffort != "" && !slices.Contains(EffortValues, req.MaxEffort) {
		return RegisterAgentProfileResp{}, domain.Validation("unknown maxEffort: "+string(req.MaxEffort), map[string]any{"field": "maxEffort"})
	}
	key, err := s.c.ownProfileKey(ctx, caller, req.ProjectID, "register_agent_profile")
	if err != nil {
		return RegisterAgentProfileResp{}, err
	}
	in := store.UpsertAgentProfileInput{ProjectID: req.ProjectID, AgentKey: key, Capabilities: req.Capabilities, Tags: req.Tags,
		Models: req.Models, CreatedBy: caller.Identity, Now: s.c.nowMs()}
	if req.MaxConcurrency > 0 {
		n := req.MaxConcurrency
		in.MaxConcurrency = &n
	}
	if req.CostClass != "" {
		c := string(req.CostClass)
		in.CostClass = &c
	}
	if req.MaxEffort != "" {
		e := string(req.MaxEffort)
		in.MaxEffort = &e
	}
	p, err := s.c.store.UpsertAgentProfile(ctx, in)
	if err != nil {
		return RegisterAgentProfileResp{}, s.c.mapErr("register_agent_profile", err)
	}
	return RegisterAgentProfileResp{Profile: profileOut(p)}, nil
}

// SkillStats is get_skill_stats: two statements, the profile and the stats.
func (s profilesSvc) SkillStats(ctx context.Context, caller domain.Caller, req GetSkillStatsReq) (GetSkillStatsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "get_skill_stats", domain.GrantSubject{}, false); err != nil {
		return GetSkillStatsResp{}, err
	}
	key := req.AgentKey
	if key == "" {
		var err error
		if key, err = agentKey(caller); err != nil {
			return GetSkillStatsResp{}, err
		}
	}
	out := GetSkillStatsResp{Stats: []SkillStat{}}
	p, err := s.c.store.GetAgentProfile(ctx, req.ProjectID, key)
	switch {
	case err == nil:
		po := profileOut(p)
		out.Profile = &po
	case isNotFound(err):
	default:
		return GetSkillStatsResp{}, s.c.mapErr("get_skill_stats", err)
	}
	stats, err := s.c.store.ListSkillStats(ctx, req.ProjectID, key)
	if err != nil {
		return GetSkillStatsResp{}, s.c.mapErr("get_skill_stats", err)
	}
	for _, st := range stats {
		var mean *float64
		if st.Samples > 0 {
			m := float64(st.TotalMs) / float64(st.Samples)
			mean = &m
		}
		out.Stats = append(out.Stats, SkillStat{AgentKey: st.AgentKey, Blocked: float64(st.Blocked), Capability: st.Capability,
			Completed: float64(st.Completed), MeanMs: mean, ProjectID: st.ProjectID, Reliability: domain.ReliabilityFor(st.Completed, st.Blocked),
			Samples: float64(st.Samples), TotalMs: float64(st.TotalMs), UpdatedAt: isoMs(st.UpdatedAt)})
	}
	return out, nil
}

func profileOut(p store.AgentProfile) AgentProfile {
	out := AgentProfile{AgentKey: p.AgentKey, Capabilities: listOrEmpty(p.Capabilities), CreatedAt: isoMs(p.CreatedAt), CreatedBy: p.CreatedBy,
		Models: listOrEmpty(p.Models), ProjectID: p.ProjectID, Tags: listOrEmpty(p.Tags), UpdatedAt: isoMs(p.UpdatedAt)}
	if p.MaxConcurrency != nil {
		n := float64(*p.MaxConcurrency)
		out.MaxConcurrency = &n
	}
	if p.CostClass != nil {
		c := CostClass(*p.CostClass)
		out.CostClass = &c
	}
	if p.MaxEffort != nil {
		e := Effort(*p.MaxEffort)
		out.MaxEffort = &e
	}
	return out
}

func listOrEmpty(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// rollUpSkill is conductor's rollUpSkillOutcome: a terminal outcome of the task cur (as loaded,
// before the transition) added to the caller's stats for each of its required capabilities, in the
// transition's transaction (conductor wrote it after the commit, best-effort; its TODO(spec) wanted
// it inside). Handle time is approximated from cur's updatedAt, the claim time absent later edits.
// No capability writes nothing. A key that is another agent's label for the caller (labelRefused's
// rule over the token rows this transaction reads), or another identity's id (namesAnotherIdentity
// through this transaction, P-351), is not the caller's to write: the outcome goes under its
// identity, as for an unusable name, and the transition stands (P-340).
func (t *txn) rollUpSkill(cur store.Task, completed bool) error {
	if len(cur.RequiredCapabilities) == 0 {
		return nil
	}
	now := t.Now.UnixMilli()
	key, err := agentKey(t.Caller)
	if err != nil {
		key = t.Caller.Identity
	} else if strings.TrimSpace(t.Caller.Agent) != "" {
		tokens, err := t.Tx.ListTokens(t.ctx, "")
		if err != nil {
			return err
		}
		named, err := namesAnotherIdentity(t.ctx, t.Tx, txMember(t.Tx), t.ProjectID, t.Caller, key)
		if err != nil {
			return err
		}
		if named || labelRefusedIn(tokens, t.Caller, key, now) {
			key = t.Caller.Identity
		}
	}
	return t.Tx.RecordSkillOutcome(t.ctx, store.SkillOutcome{ProjectID: t.ProjectID, AgentKey: key, Capabilities: cur.RequiredCapabilities,
		Completed: completed, ElapsedMs: now - cur.UpdatedAt, Now: now})
}
