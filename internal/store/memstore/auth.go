package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The areas of R0.12's contract_auth.go: users, sessions and tokens. A missing row is
// store.ErrNotFound, as on the engines.

func (s *Store) CreateUser(ctx context.Context, u store.User) error {
	return s.do(ctx, func(st *state) error {
		for _, o := range st.users {
			if o.Email == u.Email {
				return store.ErrEmailTaken
			}
		}
		if _, ok := st.users[u.ID]; ok {
			return fmt.Errorf("memstore: a user with id %s exists", u.ID)
		}
		u.DisabledAt = 0
		st.users[u.ID] = u
		return nil
	})
}

func (s *Store) GetUser(ctx context.Context, id string) (store.User, error) {
	return get(ctx, s, func(st *state) (store.User, error) {
		u, ok := st.users[id]
		if !ok {
			return store.User{}, store.ErrNotFound
		}
		return u, nil
	})
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (store.User, error) {
	return get(ctx, s, func(st *state) (store.User, error) {
		for _, u := range st.users {
			if u.Email == email {
				return u, nil
			}
		}
		return store.User{}, store.ErrNotFound
	})
}

func (s *Store) ListUsers(ctx context.Context) ([]store.User, error) {
	return get(ctx, s, func(st *state) ([]store.User, error) {
		out := make([]store.User, 0, len(st.users))
		for _, u := range st.users {
			out = append(out, u)
		}
		slices.SortFunc(out, func(a, b store.User) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
		})
		return out, nil
	})
}

func (s *Store) DisableUser(ctx context.Context, id string, at int64) error {
	return s.do(ctx, func(st *state) error {
		u, ok := st.users[id]
		if !ok {
			return store.ErrNotFound
		}
		if u.DisabledAt == 0 {
			u.DisabledAt = at
			st.users[id] = u
		}
		return nil
	})
}

func (s *Store) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	return s.do(ctx, func(st *state) error {
		u, ok := st.users[id]
		if !ok {
			return store.ErrNotFound
		}
		u.PasswordHash = passwordHash
		st.users[id] = u
		return nil
	})
}

func (s *Store) CreateSession(ctx context.Context, sess store.Session) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.users[sess.UserID]; !ok {
			return fmt.Errorf("memstore: session for unknown user %s", sess.UserID)
		}
		if _, ok := st.sessions[sess.IDHash]; ok {
			return fmt.Errorf("memstore: a session with that hash exists")
		}
		st.sessions[sess.IDHash] = sess
		return nil
	})
}

func (s *Store) GetSession(ctx context.Context, idHash string) (store.Session, error) {
	return get(ctx, s, func(st *state) (store.Session, error) {
		sess, ok := st.sessions[idHash]
		if !ok {
			return store.Session{}, store.ErrNotFound
		}
		return sess, nil
	})
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	return s.do(ctx, func(st *state) error {
		delete(st.sessions, idHash)
		return nil
	})
}

func (s *Store) CreateToken(ctx context.Context, t store.Token) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.tokens[t.ID]; ok {
			return fmt.Errorf("memstore: a token with id %s exists", t.ID)
		}
		t.LastUsedAt, t.RevokedAt = 0, 0
		st.tokens[t.ID] = t
		return nil
	})
}

func (s *Store) GetToken(ctx context.Context, id string) (store.Token, error) {
	return get(ctx, s, func(st *state) (store.Token, error) {
		t, ok := st.tokens[id]
		if !ok {
			return store.Token{}, store.ErrNotFound
		}
		return t, nil
	})
}

func (s *Store) ListTokens(ctx context.Context, identityID string) ([]store.Token, error) {
	return get(ctx, s, func(st *state) ([]store.Token, error) {
		out := []store.Token{}
		for _, t := range st.tokens {
			if identityID == "" || t.IdentityID == identityID {
				out = append(out, t)
			}
		}
		slices.SortFunc(out, func(a, b store.Token) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
		})
		return out, nil
	})
}

func (s *Store) RevokeToken(ctx context.Context, id string, at int64) error {
	return s.do(ctx, func(st *state) error {
		t, ok := st.tokens[id]
		if !ok {
			return store.ErrNotFound
		}
		if t.RevokedAt == 0 {
			t.RevokedAt = at
			st.tokens[id] = t
		}
		return nil
	})
}
