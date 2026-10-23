package memstore

import (
	"context"
	"fmt"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The OAuth area of contract_oauth.go. A row naming a missing client or family is refused, as the
// engines' foreign keys refuse it, and deleting a client deletes its codes, families and tokens, as
// their cascades do.

func cloneOAuthClient(c store.OAuthClient) store.OAuthClient {
	c.RedirectURIs = slices.Clone(c.RedirectURIs)
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	return c
}

func (st *state) needOAuthClient(id string) error {
	if _, ok := st.oauthClients[id]; !ok {
		return fmt.Errorf("memstore: oauth client %s does not exist (foreign key)", id)
	}
	return nil
}

func (s *Store) CreateOAuthClient(ctx context.Context, c store.OAuthClient) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.oauthClients[c.ClientID]; ok {
			return fmt.Errorf("memstore: oauth client %s exists", c.ClientID)
		}
		c.LastUsedAt = 0
		st.oauthClients[c.ClientID] = cloneOAuthClient(c)
		return nil
	})
}

func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (store.OAuthClient, error) {
	return get(ctx, s, func(st *state) (store.OAuthClient, error) {
		c, ok := st.oauthClients[clientID]
		if !ok {
			return store.OAuthClient{}, store.ErrNotFound
		}
		return cloneOAuthClient(c), nil
	})
}

func (s *Store) TouchOAuthClient(ctx context.Context, clientID string, at int64) error {
	return s.do(ctx, func(st *state) error {
		c, ok := st.oauthClients[clientID]
		if !ok {
			return store.ErrNotFound
		}
		c.LastUsedAt = at
		st.oauthClients[clientID] = c
		return nil
	})
}

func (s *Store) DeleteUnusedOAuthClients(ctx context.Context, createdBefore int64) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		for id, c := range st.oauthClients {
			if c.LastUsedAt != 0 || c.CreatedAt >= createdBefore {
				continue
			}
			delete(st.oauthClients, id)
			for h, code := range st.oauthCodes {
				if code.ClientID == id {
					delete(st.oauthCodes, h)
				}
			}
			for f, fam := range st.oauthFamilies {
				if fam.ClientID == id {
					delete(st.oauthFamilies, f)
				}
			}
			for h, t := range st.oauthTokens {
				if t.ClientID == id {
					delete(st.oauthTokens, h)
				}
			}
			n++
		}
		return n, nil
	})
}

func (s *Store) CreateOAuthCode(ctx context.Context, c store.OAuthCode) error {
	return s.do(ctx, func(st *state) error {
		if err := st.needOAuthClient(c.ClientID); err != nil {
			return err
		}
		if _, ok := st.oauthCodes[c.CodeHash]; ok {
			return fmt.Errorf("memstore: oauth code exists")
		}
		c.UsedAt = 0
		st.oauthCodes[c.CodeHash] = c
		return nil
	})
}

func (s *Store) ConsumeOAuthCode(ctx context.Context, codeHash string, at int64) (store.OAuthCode, error) {
	var out store.OAuthCode
	err := s.do(ctx, func(st *state) error {
		c, ok := st.oauthCodes[codeHash]
		if !ok {
			return store.ErrNotFound
		}
		out = c
		if c.UsedAt != 0 {
			return store.ErrOAuthUsed
		}
		c.UsedAt = at
		st.oauthCodes[codeHash] = c
		return nil
	})
	if err == store.ErrNotFound {
		return store.OAuthCode{}, err
	}
	return out, err
}

func (s *Store) CreateOAuthFamily(ctx context.Context, f store.OAuthFamily) error {
	return s.do(ctx, func(st *state) error {
		if err := st.needOAuthClient(f.ClientID); err != nil {
			return err
		}
		if _, ok := st.oauthFamilies[f.ID]; ok {
			return fmt.Errorf("memstore: oauth family %s exists", f.ID)
		}
		f.RevokedAt, f.RevokedReason = 0, ""
		st.oauthFamilies[f.ID] = f
		return nil
	})
}

func (s *Store) GetOAuthFamily(ctx context.Context, id string) (store.OAuthFamily, error) {
	return get(ctx, s, func(st *state) (store.OAuthFamily, error) {
		f, ok := st.oauthFamilies[id]
		if !ok {
			return store.OAuthFamily{}, store.ErrNotFound
		}
		return f, nil
	})
}

func (s *Store) RevokeOAuthFamily(ctx context.Context, id string, at int64, reason string) error {
	return s.do(ctx, func(st *state) error {
		f, ok := st.oauthFamilies[id]
		if !ok {
			return store.ErrNotFound
		}
		if f.RevokedAt == 0 {
			f.RevokedAt, f.RevokedReason = at, reason
			st.oauthFamilies[id] = f
		}
		return nil
	})
}

func (s *Store) CreateOAuthToken(ctx context.Context, t store.OAuthToken) error {
	return s.do(ctx, func(st *state) error {
		if err := st.needOAuthClient(t.ClientID); err != nil {
			return err
		}
		if _, ok := st.oauthFamilies[t.FamilyID]; !ok {
			return fmt.Errorf("memstore: oauth family %s does not exist (foreign key)", t.FamilyID)
		}
		if _, ok := st.oauthTokens[t.TokenHash]; ok {
			return fmt.Errorf("memstore: oauth token exists")
		}
		t.UsedAt, t.RevokedAt, t.FamilyRevokedAt = 0, 0, 0
		st.oauthTokens[t.TokenHash] = t
		return nil
	})
}

// withFamily is the token as the engines read it: its family's revocation time joined on.
func (st *state) withFamily(t store.OAuthToken) store.OAuthToken {
	t.FamilyRevokedAt = st.oauthFamilies[t.FamilyID].RevokedAt
	return t
}

func (s *Store) GetOAuthToken(ctx context.Context, tokenHash string) (store.OAuthToken, error) {
	return get(ctx, s, func(st *state) (store.OAuthToken, error) {
		t, ok := st.oauthTokens[tokenHash]
		if !ok {
			return store.OAuthToken{}, store.ErrNotFound
		}
		return st.withFamily(t), nil
	})
}

func (s *Store) ConsumeOAuthToken(ctx context.Context, tokenHash string, at int64) (store.OAuthToken, error) {
	var out store.OAuthToken
	err := s.do(ctx, func(st *state) error {
		t, ok := st.oauthTokens[tokenHash]
		if !ok {
			return store.ErrNotFound
		}
		out = st.withFamily(t)
		if t.UsedAt != 0 {
			return store.ErrOAuthUsed
		}
		t.UsedAt = at
		st.oauthTokens[tokenHash] = t
		return nil
	})
	if err == store.ErrNotFound {
		return store.OAuthToken{}, err
	}
	return out, err
}
