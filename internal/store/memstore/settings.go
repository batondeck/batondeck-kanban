package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The settings area of contract_settings.go.

type identityRow struct {
	kind, display string
	settings      store.IdentitySettings
}

func (s *Store) GetIdentitySettings(ctx context.Context, identityID string) (store.IdentitySettings, error) {
	return get(ctx, s, func(st *state) (store.IdentitySettings, error) {
		r, ok := st.identities[identityID]
		if !ok {
			return store.IdentitySettings{}, domain.NotFound("Identity", identityID)
		}
		out := r.settings
		out.StarredAssets = cloneStrings(out.StarredAssets)
		if out.StarredAssets == nil {
			out.StarredAssets = []string{}
		}
		return out, nil
	})
}

func (s *Store) PutIdentitySettings(ctx context.Context, in store.IdentitySettings, kind, display string) error {
	if err := store.ValidIdentitySettings(in, kind, display); err != nil {
		return err
	}
	return s.do(ctx, func(st *state) error {
		r, ok := st.identities[in.IdentityID]
		if !ok {
			r = identityRow{kind: kind, display: display}
		}
		in.StarredAssets = cloneStrings(in.StarredAssets)
		r.settings = in
		st.identities[in.IdentityID] = r
		return nil
	})
}
