package service

import (
	"context"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// access is what authorize decided: the caller's effective role in the project and the grant it
// holds. pending is set when the grant restricts task attributes and the call names a task that is
// not loaded yet: mutate finishes the check against the task it loads (permit), so a grant that
// scopes tool names alone costs no read.
type access struct {
	Role     domain.Role
	grant    domain.CapabilityGrant
	hasGrant bool
	pending  bool
	subject  domain.GrantSubject
}

// permit finishes a pending grant check against the loaded task (nil when the mutation names none).
func (a access) permit(t *store.Task) error {
	if !a.pending {
		return nil
	}
	s := a.subject
	if t != nil {
		s.Task = &domain.GrantTask{BoardID: t.BoardID, Labels: t.Labels, Priority: t.Priority}
	}
	return checkGrant(a.grant, s)
}

func checkGrant(g domain.CapabilityGrant, s domain.GrantSubject) error {
	if missing := domain.CheckGrant(g, s); missing != "" {
		return forbidden("Your capability grant does not allow this: "+missing,
			map[string]any{"requiredCapability": missing, "tool": s.Tool})
	}
	return nil
}

// authorize is the one membership, role-tier and capability-grant check (ADR-0003). Its order:
// the tool's posture (an unclassified tool is FORBIDDEN, so a new tool fails closed); a workspace
// admin flag where the posture is AdminOnly "only"; for a tenant-scoped tool, membership of
// projectID (a missing project or membership is FORBIDDEN, never NOT_FOUND, so a project's
// existence does not leak), the tier against the member's role capped by the caller's token role,
// and the grant via domain.CheckGrant unless the tool is grant-exempt. Membership and grant are
// cached per (project, identity) for the access TTL; InvalidateAccess drops an entry at once. A grant on
// a project admin is not enforced.
// AdminOnly "conditional" tools are listed and gated in their handler, not here.
func (c *Core) authorize(ctx context.Context, caller domain.Caller, projectID, tool string, subject domain.GrantSubject, namesTask bool) (access, error) {
	p, ok := domain.ToolPosture(tool)
	if !ok {
		return access{}, forbidden("Tool "+tool+" is not classified", map[string]any{"tool": tool})
	}
	if p.AdminOnly == domain.AdminOnlyOnly && !caller.WorkspaceAdmin {
		return access{}, forbidden("Workspace admin required", map[string]any{"tool": tool, "requiredCapability": "workspace-admin"})
	}
	if !p.TenantScoped {
		return access{}, nil
	}
	if projectID == "" {
		return access{}, domain.Validation("projectId is required", map[string]any{"field": "projectId"})
	}
	e, err := c.accessEntry(ctx, projectID, caller.Identity)
	if err != nil {
		return access{}, err
	}
	if !e.member {
		return access{}, forbidden("Not a member of project "+projectID, map[string]any{"projectId": projectID})
	}
	a := access{Role: effectiveRole(e.role, caller.Role), grant: e.grant, hasGrant: e.hasGrant}
	if p.Tier != "" && !domain.RoleMeetsTier(a.Role, p.Tier) {
		return access{}, forbidden("Role "+string(a.Role)+" cannot call "+tool,
			map[string]any{"tool": tool, "role": a.Role, "requiredRole": domain.TierMinRole(p.Tier)})
	}
	// A project admin can rewrite any grant, so one on an admin is not enforced (conductor's decide.ts):
	// enforcing it could lock the project's last admin out of granting.
	if !a.hasGrant || p.GrantExempt || e.role == domain.RoleAdmin {
		return a, nil
	}
	subject.Tool = tool
	if domain.GrantNeedsTask(a.grant) && subject.Task == nil && namesTask {
		a.pending, a.subject = true, subject
		return a, nil
	}
	return a, checkGrant(a.grant, subject)
}

// effectiveRole is the member's role, capped by the token's role when the token carries one (an
// OAuth scope may; a CLI token does not). An unknown token role caps to nothing.
func effectiveRole(member domain.Role, token string) domain.Role {
	if token == "" {
		return member
	}
	t := domain.Role(token)
	if domain.RoleRank(t) < domain.RoleRank(member) {
		return t
	}
	return member
}

// InvalidateAccess drops the cached membership and grant of identityID in projectID, or of every
// identity in it when identityID is "", so the next authorize reads them again. Every membership,
// role or grant change calls it (add_member, remove_member, grant_capabilities, delete_project).
func (c *Core) InvalidateAccess(projectID, identityID string) {
	c.access.invalidate(projectID, identityID)
	c.hidden.invalidate(identityID)
}

// accessEntry is the cached membership and grant, read through on a miss or an expired entry. A
// projectID longer than any the server makes is VALIDATION, neither looked up nor cached.
func (c *Core) accessEntry(ctx context.Context, projectID, identityID string) (accessCached, error) {
	if len(projectID) > maxIDLen {
		return accessCached{}, domain.Validation("projectId is not a project id", map[string]any{"field": "projectId", "maxLength": maxIDLen})
	}
	now := c.now()
	e, gen, ok := c.access.get(projectID, identityID, now)
	if ok {
		return e, nil
	}
	m, err := c.store.GetMember(ctx, projectID, identityID)
	switch be, ok := domain.AsBatonDeckError(err); {
	case err == nil:
		e.member, e.role = true, m.Role
	case ok && be.Code == domain.CodeNotFound:
	default:
		return accessCached{}, c.mapErr("authorize", err)
	}
	if e.member && c.grants != nil {
		g, ok, err := c.grants(ctx, projectID, identityID)
		if err != nil {
			return accessCached{}, c.mapErr("authorize", err)
		}
		e.grant, e.hasGrant = g, ok
	}
	c.access.put(projectID, identityID, e, gen, now)
	return e, nil
}

type accessCached struct {
	member   bool
	role     domain.Role
	grant    domain.CapabilityGrant
	hasGrant bool
	expires  time.Time
}

// accessCache holds one entry per (project, identity), a non-member included, each for ttl, and at
// most accessCacheSweep of them (put). gen
// counts invalidations: a read-through that began before one does not store what it read, so an
// invalidation is never undone by a lookup already in flight.
type accessCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	gen     uint64
	entries map[[2]string]accessCached
}

// accessCacheSweep is the size past which a put first drops the expired entries and then, while more
// than three quarters of it are left, any others: the cache never holds more, and the next sweep is
// at least a quarter of it away, so a put stays amortised O(1) however many entries are live.
const accessCacheSweep = 10_000

// maxIDLen is the longest projectId authorize looks up. The server's ids are 14 bytes (P- and 12 hex
// digits); a longer one names no project, and refusing it before the lookup keeps a caller-chosen
// string of any size out of the access cache.
const maxIDLen = 64

func newAccessCache(ttl time.Duration) *accessCache {
	return &accessCache{ttl: ttl, entries: map[[2]string]accessCached{}}
}

func (a *accessCache) get(projectID, identityID string, now time.Time) (accessCached, uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[[2]string{projectID, identityID}]
	if !ok || !now.Before(e.expires) {
		return accessCached{}, a.gen, false
	}
	return e, a.gen, true
}

func (a *accessCache) put(projectID, identityID string, e accessCached, gen uint64, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.gen {
		return
	}
	if len(a.entries) >= accessCacheSweep {
		for k, v := range a.entries {
			if !now.Before(v.expires) {
				delete(a.entries, k)
			}
		}
		for k := range a.entries {
			if len(a.entries) <= accessCacheSweep*3/4 {
				break
			}
			delete(a.entries, k)
		}
	}
	e.expires = now.Add(a.ttl)
	a.entries[[2]string{projectID, identityID}] = e
}

func (a *accessCache) invalidate(projectID, identityID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	if identityID != "" {
		delete(a.entries, [2]string{projectID, identityID})
		return
	}
	for k := range a.entries {
		if k[0] == projectID {
			delete(a.entries, k)
		}
	}
}
