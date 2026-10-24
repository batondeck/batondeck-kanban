package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// cloneGrant copies the grant's slices, keeping nil (absent) apart from empty (admits nothing).
func cloneGrant(g store.Grant) store.Grant {
	c := g.Grant
	c.Tools, c.Deny, c.Labels, c.Boards = slices.Clone(c.Tools), slices.Clone(c.Deny), slices.Clone(c.Labels), slices.Clone(c.Boards)
	c.Statuses, c.MemoryScopes = slices.Clone(c.Statuses), slices.Clone(c.MemoryScopes)
	g.Grant = c
	return g
}

func (s *Store) SetGrant(ctx context.Context, in store.SetGrantInput) (store.Grant, error) {
	if err := store.CheckGrantInput(in); err != nil {
		return store.Grant{}, err
	}
	return get(ctx, s, func(st *state) (store.Grant, error) {
		k := [2]string{in.ProjectID, in.IdentityID}
		if _, ok := st.members[k]; !ok {
			return store.Grant{}, domain.NotFound("Member", in.IdentityID)
		}
		g := cloneGrant(store.Grant{ProjectID: in.ProjectID, IdentityID: in.IdentityID, Grant: in.Grant, UpdatedBy: in.UpdatedBy, UpdatedAt: in.Now})
		st.grants[k] = g
		return cloneGrant(g), nil
	})
}

func (s *Store) GetGrant(ctx context.Context, projectID, identityID string) (store.Grant, error) {
	return get(ctx, s, func(st *state) (store.Grant, error) {
		g, ok := st.grants[[2]string{projectID, identityID}]
		if !ok {
			return store.Grant{}, domain.NotFound("Grant", identityID)
		}
		return cloneGrant(g), nil
	})
}

func (s *Store) DeleteGrant(ctx context.Context, projectID, identityID string) (bool, error) {
	return get(ctx, s, func(st *state) (bool, error) {
		k := [2]string{projectID, identityID}
		_, ok := st.grants[k]
		delete(st.grants, k)
		return ok, nil
	})
}

func (s *Store) ListMemberGrants(ctx context.Context, identityID string) ([]store.MemberGrant, error) {
	return get(ctx, s, func(st *state) ([]store.MemberGrant, error) {
		out := []store.MemberGrant{}
		for k, m := range st.members {
			if k[1] != identityID {
				continue
			}
			mg := store.MemberGrant{ProjectID: k[0], Role: m.Role}
			if g, ok := st.grants[k]; ok {
				c := cloneGrant(g).Grant
				mg.Grant = &c
			}
			out = append(out, mg)
		}
		slices.SortFunc(out, func(a, b store.MemberGrant) int { return strings.Compare(a.ProjectID, b.ProjectID) })
		return out, nil
	})
}
