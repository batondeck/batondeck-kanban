package service

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// projects is service.Projects; its membership half is members.go. Every mutation goes through
// mutate, every check through authorize.
type projects struct{ c *Core }

var _ Projects = projects{}

// Projects is the projects, settings and membership service.
func (c *Core) Projects() Projects { return projects{c} }

func (p projects) Create(ctx context.Context, caller domain.Caller, req CreateProjectReq) (CreateProjectResp, error) {
	if _, err := p.c.authorize(ctx, caller, "", "create_project", domain.GrantSubject{}, false); err != nil {
		return CreateProjectResp{}, err
	}
	if err := required("name", req.Name); err != nil {
		return CreateProjectResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateProjectResp{}, err
	}
	// ProjectID "" keeps the idempotency key under store.GlobalScope; fn names the new project so the
	// change-log row is scoped to it.
	return mutate(ctx, p.c, caller, mutation{
		Tool: "create_project", Args: map[string]any{"name": req.Name}, IdempotencyKey: req.IdempotencyKey,
	}, func(t *txn) (CreateProjectResp, error) {
		pr, err := t.Tx.CreateProject(ctx, store.CreateProjectInput{Name: req.Name, CreatedBy: caller.Identity}, store.WriteOptions{Identity: caller.Identity})
		if err != nil {
			return CreateProjectResp{}, err
		}
		t.ProjectID = pr.ID
		// DEVIATION: the contract names a project.created event; the store's Events area appends only
		// task and board events (AppendBoardEvent requires a board), so none is written.
		return CreateProjectResp{ProjectID: pr.ID}, nil
	})
}

func (p projects) Get(ctx context.Context, caller domain.Caller, req GetProjectReq) (GetProjectResp, error) {
	if _, err := p.c.authorize(ctx, caller, req.ProjectID, "get_project", domain.GrantSubject{}, false); err != nil {
		return GetProjectResp{}, err
	}
	pr, err := p.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return GetProjectResp{}, p.c.mapErr("get_project", err)
	}
	members, err := p.c.store.ListMembers(ctx, req.ProjectID)
	if err != nil {
		return GetProjectResp{}, p.c.mapErr("get_project", err)
	}
	display, err := p.c.displayNames(ctx)
	if err != nil {
		return GetProjectResp{}, p.c.mapErr("get_project", err)
	}
	out, err := projectOut(pr, members)
	if err != nil {
		return GetProjectResp{}, p.c.mapErr("get_project", err)
	}
	return GetProjectResp{Project: out, MemberDisplay: memberDisplay(members, display)}, nil
}

func (p projects) List(ctx context.Context, caller domain.Caller, req ListProjectsReq) (ListProjectsResp, error) {
	if _, err := p.c.authorize(ctx, caller, "", "list_projects", domain.GrantSubject{}, false); err != nil {
		return ListProjectsResp{}, err
	}
	if req.All {
		if !caller.WorkspaceAdmin {
			return ListProjectsResp{}, forbidden("Workspace admin required for all:true", map[string]any{"requiredCapability": "workspace-admin"})
		}
		// The store has no read of every project; refusing beats answering with the admin's own.
		return ListProjectsResp{}, domain.Validation("list_projects all:true is not available: the store has no read of every project", map[string]any{"field": "all"})
	}
	mine, err := p.c.store.ListProjects(ctx, caller.Identity)
	if err != nil {
		return ListProjectsResp{}, p.c.mapErr("list_projects", err)
	}
	display, err := p.c.displayNames(ctx)
	if err != nil {
		return ListProjectsResp{}, p.c.mapErr("list_projects", err)
	}
	out := ListProjectsResp{Projects: make([]ProjectListing, 0, len(mine))}
	for _, pm := range mine {
		// ponytail: one ListMembers per project (the member map is on every row); a store read of
		// members across projects would make it one.
		members, err := p.c.store.ListMembers(ctx, pm.Project.ID)
		if err != nil {
			return ListProjectsResp{}, p.c.mapErr("list_projects", err)
		}
		s, err := settingsOut(pm.Project.Settings)
		if err != nil {
			return ListProjectsResp{}, p.c.mapErr("list_projects", err)
		}
		roles := memberRoles(members)
		md := memberDisplay(members, display)
		row := ProjectListing{
			ProjectID: pm.Project.ID, Name: pm.Project.Name, Role: pm.Role, QuotaEnabled: s.QuotaEnabled,
			TxnCap: s.TxnCap, Members: &roles, MemberDisplay: &md,
		}
		if s.Color != "" {
			row.Color = &s.Color
		}
		if i := slices.IndexFunc(members, func(m store.Member) bool { return m.Role == domain.RoleAdmin }); i >= 0 {
			owner := members[i].IdentityID
			row.Owner = &owner
			if d, ok := display[owner]; ok {
				row.OwnerDisplay = &d
			}
		}
		out.Projects = append(out.Projects, row)
	}
	return out, nil
}

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// UpdateSettings is update_settings. There is no transitionTable: the request has no such field,
// and the store refuses a patch naming one.
func (p projects) UpdateSettings(ctx context.Context, caller domain.Caller, req UpdateSettingsReq) (UpdateSettingsResp, error) {
	a, err := p.c.authorize(ctx, caller, req.ProjectID, "update_settings", domain.GrantSubject{}, false)
	if err != nil {
		return UpdateSettingsResp{}, err
	}
	patch, err := settingsPatch(caller, req)
	if err != nil {
		return UpdateSettingsResp{}, err
	}
	args, err := argsOf(req)
	if err != nil {
		return UpdateSettingsResp{}, p.c.mapErr("update_settings", err)
	}
	return mutate(ctx, p.c, caller, mutation{Tool: "update_settings", Args: args, ProjectID: req.ProjectID, Access: a}, func(t *txn) (UpdateSettingsResp, error) {
		pr, err := t.Tx.UpdateSettings(ctx, req.ProjectID, patch)
		if err != nil {
			return UpdateSettingsResp{}, err
		}
		s, err := settingsOut(pr.Settings)
		if err != nil {
			return UpdateSettingsResp{}, err
		}
		return UpdateSettingsResp{Settings: s}, nil
	})
}

// settingsPatch validates the request as conductor's update_settings schema does and turns each
// present field into the store's patch. A field is present when it is not its zero value (the DTO's
// omitzero), so false, 0 and null cannot be sent.
func settingsPatch(caller domain.Caller, req UpdateSettingsReq) (store.SettingsPatch, error) {
	bad := func(field, msg string) error {
		return domain.Validation(field+" "+msg, map[string]any{"field": field})
	}
	if req.QuotaEnabled || req.TxnCap != nil {
		if !caller.WorkspaceAdmin {
			return nil, forbidden("quotaEnabled/txnCap are workspace-admin only", map[string]any{"requiredCapability": "workspace-admin"})
		}
		// Metering and quota are dropped (plan §Target surface): a cap would be recorded and bind
		// nothing, which conductor refuses when its deployment does not enforce quotas.
		return nil, domain.Validation("Quota enforcement is not part of this deployment, so a txnCap would be recorded and never enforced", map[string]any{"quotaEnabled": false})
	}
	if req.AgingCeilingMs < 0 {
		return nil, bad("agingCeilingMs", "must be a positive integer")
	}
	if req.MaxRunsPerTask < 0 || req.MaxRunsPerTask > 50 {
		return nil, bad("maxRunsPerTask", "must be an integer from 1 to 50")
	}
	if req.MaxTicketTokens != 0 && (req.MaxTicketTokens < 100 || req.MaxTicketTokens > 1_000_000) {
		return nil, bad("maxTicketTokens", "must be an integer from 100 to 1000000")
	}
	for field, mode := range map[string]PolicyMode{"artifactPolicy": req.ArtifactPolicy, "checkpointPolicy": req.CheckpointPolicy, "selfApprovalPolicy": req.SelfApprovalPolicy, "tokenPolicy": req.TokenPolicy} {
		if mode != "" && !slices.Contains(PolicyModeValues, mode) {
			return nil, bad(field, "must be one of off, warn, enforce")
		}
	}
	if req.Color != nil && !colorPattern.MatchString(*req.Color) {
		return nil, bad("color", "must be a #rrggbb hex colour")
	}
	if req.DefaultReviewer != nil && (*req.DefaultReviewer == "" || len(*req.DefaultReviewer) > 320) {
		return nil, bad("defaultReviewer", "must be 1 to 320 characters")
	}
	ap := req.AnomalyPolicy
	if ap.ClaimThrashCount < 0 || ap.ReworkLoopCount < 0 || ap.StaleInProgressMs < 0 || ap.StarvedUrgentMs < 0 || ap.WindowSec < 0 || ap.WIPJamMinTasks < 0 {
		return nil, bad("anomalyPolicy", "thresholds must be positive integers")
	}
	rp := req.RetryPolicy
	if rp != (RetryPolicyPatch{}) {
		if rp.BaseDelayMs < 0 || rp.MaxDelayMs < 0 || rp.PoisonAfterAttempts <= 0 || rp.Jitter < 0 || rp.Jitter > 1 || math.IsNaN(rp.Jitter) {
			return nil, bad("retryPolicy", "needs baseDelayMs, maxDelayMs ≥ 0, jitter in [0, 1] and poisonAfterAttempts > 0")
		}
		if rp.MaxDelayMs < rp.BaseDelayMs {
			return nil, bad("retryPolicy", "maxDelayMs must be ≥ baseDelayMs")
		}
	}
	sw := req.SelectionWeights
	if sw != (SelectionWeightsPatch{}) {
		ws := []float64{sw.Aging, sw.Capability, sw.Deadline, sw.Priority, sw.Readiness}
		if slices.ContainsFunc(ws, func(w float64) bool { return w < 0 || math.IsNaN(w) || math.IsInf(w, 0) }) {
			return nil, bad("selectionWeights", "must be finite and non-negative")
		}
	}

	patch := store.SettingsPatch{}
	set := func(key string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		patch[key] = raw
		return nil
	}
	var err error
	add := func(present bool, key string, v any) {
		if present && err == nil {
			err = set(key, v)
		}
	}
	add(req.ReviewEnabled, "reviewEnabled", true)
	add(req.AgingCeilingMs != 0, "agingCeilingMs", req.AgingCeilingMs)
	add(ap != (AnomalyPolicyPatch{}), "anomalyPolicy", ap)
	add(req.ArtifactPolicy != "", "artifactPolicy", req.ArtifactPolicy)
	add(len(req.CapabilityAliases) > 0, "capabilityAliases", req.CapabilityAliases)
	add(req.CheckpointPolicy != "", "checkpointPolicy", req.CheckpointPolicy)
	add(req.Color != nil, "color", req.Color)
	add(req.DefaultReviewer != nil, "defaultReviewer", req.DefaultReviewer)
	add(req.MaxRunsPerTask != 0, "maxRunsPerTask", req.MaxRunsPerTask)
	add(req.MaxTicketTokens != 0, "maxTicketTokens", req.MaxTicketTokens)
	add(rp != (RetryPolicyPatch{}), "retryPolicy", domain.RetryPolicy{BaseDelayMs: rp.BaseDelayMs, MaxDelayMs: rp.MaxDelayMs, Jitter: rp.Jitter, PoisonAfterAttempts: int(rp.PoisonAfterAttempts)})
	add(sw != (SelectionWeightsPatch{}), "selectionWeights", sw)
	add(req.SelfApprovalPolicy != "", "selfApprovalPolicy", req.SelfApprovalPolicy)
	add(req.TokenPolicy != "", "tokenPolicy", req.TokenPolicy)
	if err != nil {
		return nil, err
	}
	return patch, nil
}

// argsOf is a request as the call's arguments: its present fields by their wire names, which is
// what domain.DescribeAudit picks from.
func argsOf(req any) (map[string]any, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

// Delete is delete_project: the project and everything it owns, in one transaction. A workspace
// admin may delete a project it is not a member of (the posture's conditional branch); anyone else
// needs the project's admin role.
func (p projects) Delete(ctx context.Context, caller domain.Caller, req DeleteProjectReq) (DeleteProjectResp, error) {
	var a access
	if caller.WorkspaceAdmin {
		if err := required("projectId", req.ProjectID); err != nil {
			return DeleteProjectResp{}, err
		}
		if _, err := p.c.store.GetProject(ctx, req.ProjectID); err != nil {
			return DeleteProjectResp{}, p.c.mapErr("delete_project", err)
		}
	} else {
		var err error
		if a, err = p.c.authorize(ctx, caller, req.ProjectID, "delete_project", domain.GrantSubject{}, false); err != nil {
			return DeleteProjectResp{}, err
		}
	}
	resp, err := mutate(ctx, p.c, caller, mutation{
		Tool: "delete_project", Args: map[string]any{"projectId": req.ProjectID}, ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (DeleteProjectResp, error) {
		// No event: the project's event log goes with it; the audit row is the trace.
		if err := t.Tx.DeleteProjectRecursive(ctx, req.ProjectID); err != nil {
			return DeleteProjectResp{}, err
		}
		return DeleteProjectResp{Ok: true}, nil
	})
	if err != nil {
		return DeleteProjectResp{}, err
	}
	p.c.InvalidateAccess(req.ProjectID, "")
	p.c.dropSnapshots(req.ProjectID, "")
	return resp, nil
}

// displayNames is every user's display name (else login) by id: one ListUsers, bounded by the
// workspace's accounts.
func (c *Core) displayNames(ctx context.Context) (map[string]string, error) {
	users, err := c.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(users))
	for _, u := range users {
		switch {
		case u.DisplayName != "":
			out[u.ID] = u.DisplayName
		case u.Email != "":
			out[u.ID] = u.Email
		}
	}
	return out, nil
}

func memberRoles(members []store.Member) map[string]domain.Role {
	out := make(map[string]domain.Role, len(members))
	for _, m := range members {
		out[m.IdentityID] = m.Role
	}
	return out
}

// memberDisplay is the members' labels; a member with no account has none (render its id).
func memberDisplay(members []store.Member, display map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range members {
		if d, ok := display[m.IdentityID]; ok {
			out[m.IdentityID] = d
		}
	}
	return out
}

func projectOut(pr store.Project, members []store.Member) (Project, error) {
	s, err := settingsOut(pr.Settings)
	if err != nil {
		return Project{}, err
	}
	return Project{
		ID: pr.ID, Name: pr.Name, CreatedBy: pr.CreatedBy, CreatedAt: isoMs(pr.CreatedAt),
		Members: memberRoles(members), Settings: s,
	}, nil
}

// settingsOut is the store's settings as the schema's object: the named settings, reviewEnabled and
// the retry policy when the project has one of its own.
func settingsOut(ps store.ProjectSettings) (Settings, error) {
	m := make(map[string]json.RawMessage, len(ps.Other)+2)
	for k, v := range ps.Other {
		m[k] = v
	}
	var err error
	if m["reviewEnabled"], err = json.Marshal(ps.ReviewEnabled); err != nil {
		return Settings{}, err
	}
	if ps.RetryPolicy != nil {
		if m["retryPolicy"], err = json.Marshal(ps.RetryPolicy); err != nil {
			return Settings{}, err
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}
