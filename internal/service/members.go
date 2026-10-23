package service

import (
	"context"
	"errors"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// AddMember is add_member: add or change a member's role. Demoting the project's last admin is
// VALIDATION (the store leaves that policy to its caller). After the commit the identity's cached
// access is dropped, so a new role or membership holds on its next call.
func (p projects) AddMember(ctx context.Context, caller domain.Caller, req AddMemberReq) (AddMemberResp, error) {
	a, err := p.c.authorize(ctx, caller, req.ProjectID, "add_member", domain.GrantSubject{}, false)
	if err != nil {
		return AddMemberResp{}, err
	}
	if _, err := domain.ParseRole(string(req.Role)); err != nil {
		return AddMemberResp{}, err
	}
	id, err := p.c.resolveIdentity(ctx, req.IdentityID)
	if err != nil {
		return AddMemberResp{}, err
	}
	resp, err := mutate(ctx, p.c, caller, mutation{
		Tool: "add_member", Args: map[string]any{"projectId": req.ProjectID, "identityId": id, "role": string(req.Role)},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (AddMemberResp, error) {
		members, err := t.Tx.ListMembers(ctx, req.ProjectID)
		if err != nil {
			return AddMemberResp{}, err
		}
		roles := memberRoles(members)
		if req.Role != domain.RoleAdmin && roles[id] == domain.RoleAdmin && admins(roles) <= 1 {
			return AddMemberResp{}, domain.Validation("Cannot demote the last admin", map[string]any{"identityId": id})
		}
		if _, err := t.Tx.AddMember(ctx, req.ProjectID, id, req.Role, caller.Identity); err != nil {
			return AddMemberResp{}, err
		}
		roles[id] = req.Role
		return AddMemberResp{Members: roles}, nil
	})
	if err != nil {
		return AddMemberResp{}, err
	}
	p.c.InvalidateAccess(req.ProjectID, id)
	return resp, nil
}

// RemoveMember is remove_member; removing the last admin is VALIDATION, a non-member NOT_FOUND.
// After the commit the identity's cached access is dropped, so it is refused on its next call.
func (p projects) RemoveMember(ctx context.Context, caller domain.Caller, req RemoveMemberReq) (RemoveMemberResp, error) {
	a, err := p.c.authorize(ctx, caller, req.ProjectID, "remove_member", domain.GrantSubject{}, false)
	if err != nil {
		return RemoveMemberResp{}, err
	}
	id, err := p.c.resolveIdentity(ctx, req.IdentityID)
	if err != nil {
		return RemoveMemberResp{}, err
	}
	resp, err := mutate(ctx, p.c, caller, mutation{
		Tool: "remove_member", Args: map[string]any{"projectId": req.ProjectID, "identityId": id},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (RemoveMemberResp, error) {
		members, err := t.Tx.ListMembers(ctx, req.ProjectID)
		if err != nil {
			return RemoveMemberResp{}, err
		}
		roles := memberRoles(members)
		if roles[id] == domain.RoleAdmin && admins(roles) <= 1 {
			return RemoveMemberResp{}, domain.Validation("Cannot remove the last admin", map[string]any{"identityId": id})
		}
		if err := t.Tx.RemoveMember(ctx, req.ProjectID, id); err != nil {
			return RemoveMemberResp{}, err
		}
		return RemoveMemberResp{Ok: true}, nil
	})
	if err != nil {
		return RemoveMemberResp{}, err
	}
	p.c.InvalidateAccess(req.ProjectID, id)
	return resp, nil
}

func admins(roles map[string]domain.Role) int {
	n := 0
	for _, r := range roles {
		if r == domain.RoleAdmin {
			n++
		}
	}
	return n
}

// resolveIdentity is the member key an identityId names: the user id itself, or the id of the user
// whose login it is (an admin knows the login; the membership is keyed by the id a token resolves
// to). Any other reference is kept as given, trimmed: a member need not be an account yet.
func (c *Core) resolveIdentity(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if err := required("identityId", ref); err != nil {
		return "", err
	}
	if _, err := c.store.GetUser(ctx, ref); err == nil {
		return ref, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", c.mapErr("resolve identity", err)
	}
	u, err := c.store.GetUserByEmail(ctx, strings.ToLower(ref))
	switch {
	case err == nil:
		return u.ID, nil
	case errors.Is(err, store.ErrNotFound):
		return ref, nil
	default:
		return "", c.mapErr("resolve identity", err)
	}
}
