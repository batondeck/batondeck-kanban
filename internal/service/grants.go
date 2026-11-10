package service

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Grants is the capability grants service.
func (c *Core) Grants() Grants { return grantsSvc{c} }

type grantsSvc struct{ c *Core }

// Grant is grant_capabilities. The identity resolves as remove_member's does (memberKey); one that is not a member
// is VALIDATION (conductor's), a deny or tools entry that is no tool, or one no grant can enforce, is
// VALIDATION naming it (domain.CheckGrantEnforceable; a grant stored before that check still loads
// and is enforced by the names it holds). After the
// commit the member's cached access and tools/list narrowing are dropped, so the grant holds on their
// next call and their next request is told its tool list changed.
func (g grantsSvc) Grant(ctx context.Context, caller domain.Caller, req GrantCapabilitiesReq) (GrantCapabilitiesResp, error) {
	a, err := g.c.authorize(ctx, caller, req.ProjectID, "grant_capabilities", domain.GrantSubject{}, false)
	if err != nil {
		return GrantCapabilitiesResp{}, err
	}
	id, err := g.c.memberKey(ctx, req.ProjectID, req.IdentityID)
	if err != nil {
		return GrantCapabilitiesResp{}, err
	}
	var grant *domain.CapabilityGrant
	switch {
	case !req.Scope.Set:
		d := domain.WorkerDefaultGrant()
		grant = &d
	case !req.Scope.Null:
		d := grantIn(req.Scope.V)
		grant = &d
	}
	var scope any
	if grant != nil {
		if err := domain.CheckGrantEnforceable(*grant); err != nil {
			return GrantCapabilitiesResp{}, err
		}
		scope = grantOut(*grant)
	}
	resp, err := mutate(ctx, g.c, caller, mutation{
		Tool: "grant_capabilities", Args: map[string]any{"projectId": req.ProjectID, "identityId": id, "scope": scope},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (GrantCapabilitiesResp, error) {
		if _, err := t.Tx.GetMember(ctx, req.ProjectID, id); err != nil {
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
				return GrantCapabilitiesResp{}, domain.Validation("Not a member of project "+req.ProjectID, map[string]any{"identityId": id})
			}
			return GrantCapabilitiesResp{}, err
		}
		out := GrantCapabilitiesResp{IdentityID: id}
		if grant == nil {
			_, err := t.Tx.DeleteGrant(ctx, req.ProjectID, id)
			return out, err
		}
		row, err := t.Tx.SetGrant(ctx, store.SetGrantInput{ProjectID: req.ProjectID, IdentityID: id, Grant: *grant,
			UpdatedBy: caller.Identity, Now: t.Now.UnixMilli()})
		if err != nil {
			return GrantCapabilitiesResp{}, err
		}
		o := grantOut(row.Grant)
		out.Grant = &o
		return out, nil
	})
	if err != nil {
		return GrantCapabilitiesResp{}, err
	}
	g.c.InvalidateAccess(req.ProjectID, id)
	return resp, nil
}

func grantIn(g Grant) domain.CapabilityGrant {
	return domain.CapabilityGrant{Tools: g.Tools, Deny: g.Deny, Labels: g.Labels, Boards: g.Boards, MaxPriority: g.MaxPriority,
		Statuses: g.Statuses, MemoryScopes: g.MemoryScopes}
}

func grantOut(g domain.CapabilityGrant) Grant {
	return Grant{Tools: g.Tools, Deny: g.Deny, Labels: g.Labels, Boards: g.Boards, MaxPriority: g.MaxPriority,
		Statuses: g.Statuses, MemoryScopes: g.MemoryScopes}
}

// storeGrant is the grant reader authorize uses when Options.Grants is nil: the store's row.
func (c *Core) storeGrant(ctx context.Context, projectID, identityID string) (domain.CapabilityGrant, bool, error) {
	g, err := c.store.GetGrant(ctx, projectID, identityID)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return domain.CapabilityGrant{}, false, nil
	}
	if err != nil {
		return domain.CapabilityGrant{}, false, err
	}
	return g.Grant, true, nil
}

// HiddenTools is the tools internal/mcp leaves out of the caller's tools/list: the governed project
// tools the grant of every membership refuses by name (domain.GrantHiddenTools), a project admin's
// grant counting as none. It is one statement per identity per access TTL, cached beside the access
// entries and dropped with them by InvalidateAccess. It decides visibility only, never authority.
func (c *Core) HiddenTools(ctx context.Context, caller domain.Caller) ([]string, error) {
	if caller.Identity == "" {
		return nil, nil
	}
	now := c.now()
	tools, gen, ok := c.hidden.get(caller.Identity, now)
	if ok {
		return tools, nil
	}
	rows, err := c.store.ListMemberGrants(ctx, caller.Identity)
	if err != nil {
		return nil, c.mapErr("hidden tools", err)
	}
	grants := make([]domain.CapabilityGrant, 0, len(rows))
	for _, m := range rows {
		var g domain.CapabilityGrant
		if m.Grant != nil && m.Role != domain.RoleAdmin {
			g = *m.Grant
		}
		grants = append(grants, g)
	}
	tools = domain.GrantHiddenTools(grants)
	c.hidden.put(caller.Identity, tools, gen, now, c.access.ttl)
	return tools, nil
}

// hiddenCache is HiddenTools' answer per identity; gen counts invalidations, as accessCache's does,
// so a read begun before one never stores what it read.
type hiddenCache struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]hiddenEntry
}

type hiddenEntry struct {
	tools   []string
	expires time.Time
}

func (h *hiddenCache) get(identityID string, now time.Time) ([]string, uint64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.entries[identityID]
	if !ok || !now.Before(e.expires) {
		return nil, h.gen, false
	}
	return slices.Clone(e.tools), h.gen, true
}

func (h *hiddenCache) put(identityID string, tools []string, gen uint64, now time.Time, ttl time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if gen != h.gen {
		return
	}
	if len(h.entries) >= accessCacheSweep {
		for k, v := range h.entries {
			if !now.Before(v.expires) {
				delete(h.entries, k)
			}
		}
	}
	h.entries[identityID] = hiddenEntry{tools: slices.Clone(tools), expires: now.Add(ttl)}
}

// invalidate drops identityID's entry, or every entry when it is "".
func (h *hiddenCache) invalidate(identityID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gen++
	if identityID == "" {
		clear(h.entries)
		return
	}
	delete(h.entries, identityID)
}
