package service

import (
	"context"
	"errors"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// AddMember is add_member: add or change a member's role. Demoting the project's last admin is
// VALIDATION (the store leaves that policy to its caller); the guard reads the member list and
// writes one row, so add_member and remove_member hold the project lock (mutation.LockProject):
// under Postgres READ COMMITTED two admins demoting or removing each other at once would each count
// two admins and leave none. When the identity is an account, a row
// keyed by its login (added by that login before the account existed, and kept as given) is folded
// into the account's row in the same mutation: the row goes, and its grant follows when the account
// was no member. After the commit the identity's cached access is dropped (InvalidateAccess here,
// ObserveChange in every other process once the bus delivers the row), so a new role or membership
// holds on its next call.
func (p projects) AddMember(ctx context.Context, caller domain.Caller, req AddMemberReq) (AddMemberResp, error) {
	a, err := p.c.authorize(ctx, caller, req.ProjectID, "add_member", domain.GrantSubject{}, false)
	if err != nil {
		return AddMemberResp{}, err
	}
	if _, err := domain.ParseRole(string(req.Role)); err != nil {
		return AddMemberResp{}, err
	}
	id, login, err := p.c.resolveIdentity(ctx, req.IdentityID)
	if err != nil {
		return AddMemberResp{}, err
	}
	resp, err := mutate(ctx, p.c, caller, mutation{
		Tool: "add_member", Args: map[string]any{"projectId": req.ProjectID, "identityId": id, "role": string(req.Role)},
		ProjectID: req.ProjectID, Access: a, LockProject: true,
	}, func(t *txn) (AddMemberResp, error) {
		members, err := t.Tx.ListMembers(ctx, req.ProjectID)
		if err != nil {
			return AddMemberResp{}, err
		}
		roles := memberRoles(members)
		if req.Role != domain.RoleAdmin && roles[id] == domain.RoleAdmin {
			if ok, err := keepsAnAdmin(ctx, t.Tx, roles, id); err != nil {
				return AddMemberResp{}, err
			} else if !ok {
				return AddMemberResp{}, domain.Validation("Cannot demote the last admin", map[string]any{"identityId": id})
			}
		}
		_, wasMember := roles[id]
		if _, err := t.Tx.AddMember(ctx, req.ProjectID, id, req.Role, caller.Identity); err != nil {
			return AddMemberResp{}, err
		}
		for k := range roles {
			if login == "" || k == id || !strings.EqualFold(strings.TrimSpace(k), login) {
				continue
			}
			if err := foldMember(ctx, t, req.ProjectID, k, id, !wasMember); err != nil {
				return AddMemberResp{}, err
			}
			delete(roles, k)
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

// foldMember removes the member row keyed by from, a login now an account's, whose row is to; when
// moveGrant is set (to was no member before this call) the grant from held is written for to, so a
// grant set up for the login still narrows the account.
func foldMember(ctx context.Context, t *txn, projectID, from, to string, moveGrant bool) error {
	if moveGrant {
		g, err := t.Tx.GetGrant(ctx, projectID, from)
		switch be, ok := domain.AsBatonDeckError(err); {
		case err == nil:
			if _, err := t.Tx.SetGrant(ctx, store.SetGrantInput{ProjectID: projectID, IdentityID: to, Grant: g.Grant,
				UpdatedBy: t.Caller.Identity, Now: t.Now.UnixMilli()}); err != nil {
				return err
			}
		case !ok || be.Code != domain.CodeNotFound:
			return err
		}
	}
	return t.Tx.RemoveMember(ctx, projectID, from)
}

// RemoveMember is remove_member; removing the last admin is VALIDATION, a non-member NOT_FOUND. The
// identity is named as memberKey resolves it, so a row keyed by a login is removable by that login
// after an account has taken it.
// After the commit the identity's cached access is dropped (InvalidateAccess here, ObserveChange in
// every other process once the bus delivers the row), so it is refused on its next call.
func (p projects) RemoveMember(ctx context.Context, caller domain.Caller, req RemoveMemberReq) (RemoveMemberResp, error) {
	a, err := p.c.authorize(ctx, caller, req.ProjectID, "remove_member", domain.GrantSubject{}, false)
	if err != nil {
		return RemoveMemberResp{}, err
	}
	id, err := p.c.memberKey(ctx, req.ProjectID, req.IdentityID)
	if err != nil {
		return RemoveMemberResp{}, err
	}
	resp, err := mutate(ctx, p.c, caller, mutation{
		Tool: "remove_member", Args: map[string]any{"projectId": req.ProjectID, "identityId": id},
		ProjectID: req.ProjectID, Access: a, LockProject: true,
	}, func(t *txn) (RemoveMemberResp, error) {
		members, err := t.Tx.ListMembers(ctx, req.ProjectID)
		if err != nil {
			return RemoveMemberResp{}, err
		}
		roles := memberRoles(members)
		if roles[id] == domain.RoleAdmin {
			if ok, err := keepsAnAdmin(ctx, t.Tx, roles, id); err != nil {
				return RemoveMemberResp{}, err
			} else if !ok {
				return RemoveMemberResp{}, domain.Validation("Cannot remove the last admin", map[string]any{"identityId": id})
			}
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

// keepsAnAdmin reports whether the project keeps an admin some caller can act as once id is no
// longer one: another admin row keyed by an account id, or by a reference that is no account's login
// (kept as given: a member need not be an account yet). An admin row keyed by the login of an account
// is not counted: every reference to that login resolves to the account's id, so no caller is that
// member, and counting it would let the project's last real admin leave.
func keepsAnAdmin(ctx context.Context, tx store.Store, roles map[string]domain.Role, id string) (bool, error) {
	for k, r := range roles {
		if k == id || r != domain.RoleAdmin {
			continue
		}
		if _, err := tx.GetUser(ctx, k); err == nil {
			return true, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
		switch _, err := tx.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(k))); {
		case errors.Is(err, store.ErrNotFound):
			return true, nil
		case err != nil:
			return false, err
		}
	}
	return false, nil
}

// memberKey is the member key an identityId names in projectID for a call on an existing member
// (remove_member, grant_capabilities): the reference itself, trimmed, when a member row has exactly
// that key, else resolveIdentity's. A row added by a login before its account existed keeps that
// key, and the login resolves to the account's id once the account exists: without this the row
// could not be named, and Remove on it would remove the account's own membership instead.
func (c *Core) memberKey(ctx context.Context, projectID, ref string) (string, error) {
	if k := strings.TrimSpace(ref); k != "" {
		_, err := c.store.GetMember(ctx, projectID, k)
		if err == nil {
			return k, nil
		}
		if be, ok := domain.AsBatonDeckError(err); !ok || be.Code != domain.CodeNotFound {
			return "", c.mapErr("resolve identity", err)
		}
	}
	id, _, err := c.resolveIdentity(ctx, ref)
	return id, err
}

// resolveIdentity is the member key an identityId names: the user id itself, or the id of the user
// whose login it is (an admin knows the login; the membership is keyed by the id a token resolves
// to), with that account's login. Any other reference is kept as given, trimmed, with login "": a
// member need not be an account yet.
func (c *Core) resolveIdentity(ctx context.Context, ref string) (id, login string, err error) {
	ref = strings.TrimSpace(ref)
	if err := required("identityId", ref); err != nil {
		return "", "", err
	}
	if u, err := c.store.GetUser(ctx, ref); err == nil {
		return ref, u.Email, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", "", c.mapErr("resolve identity", err)
	}
	u, err := c.store.GetUserByEmail(ctx, strings.ToLower(ref))
	switch {
	case err == nil:
		return u.ID, u.Email, nil
	case errors.Is(err, store.ErrNotFound):
		return ref, "", nil
	default:
		return "", "", c.mapErr("resolve identity", err)
	}
}
