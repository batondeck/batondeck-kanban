package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The auth areas' handles: inside Atomically every statement, reads included, goes through the
// transaction; outside it SQLite reads use the read pool and writes the one write connection.

func (s *SQLite) authRead() *sqlitedb.Queries {
	if s.tx != nil {
		return sqlitedb.New(s.tx)
	}
	return sqlitedb.New(s.r)
}

func (s *SQLite) authWrite() *sqlitedb.Queries {
	if s.tx != nil {
		return sqlitedb.New(s.tx)
	}
	return sqlitedb.New(s.w)
}

func (s *Postgres) authQ() *pgdb.Queries {
	if s.tx != nil {
		return pgdb.New(s.tx)
	}
	return pgdb.New(s.pool)
}

// authErr maps either driver's no-row error to ErrNotFound and names the area on the rest.
func authErr(op string, err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("store: %s: %w", op, err)
}

func nullMs(n sql.NullInt64) int64 {
	if n.Valid {
		return n.Int64
	}
	return 0
}

func userFromRow(r sqlitedb.User) User {
	return User{ID: r.ID, Email: r.Email, DisplayName: r.DisplayName, PasswordHash: r.PasswordHash,
		CreatedAt: r.CreatedAt, DisabledAt: nullMs(r.DisabledAt)}
}

func usersFromRows(rows []sqlitedb.User) []User {
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		out = append(out, userFromRow(r))
	}
	return out
}

func insertUserParams(u User) sqlitedb.InsertUserParams {
	return sqlitedb.InsertUserParams{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
		PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt}
}

func disableParams(id string, at int64) sqlitedb.DisableUserParams {
	return sqlitedb.DisableUserParams{DisabledAt: sql.NullInt64{Int64: at, Valid: true}, ID: id}
}

// --- SQLite ---

func (s *SQLite) CreateUser(ctx context.Context, u User) error {
	n, err := s.authWrite().InsertUser(ctx, insertUserParams(u))
	if err != nil {
		return authErr("create user", err)
	}
	if n == 0 {
		return ErrEmailTaken
	}
	return nil
}

func (s *SQLite) GetUser(ctx context.Context, id string) (User, error) {
	r, err := s.authRead().GetUser(ctx, id)
	if err != nil {
		return User{}, authErr("get user", err)
	}
	return userFromRow(r), nil
}

func (s *SQLite) GetUserByEmail(ctx context.Context, email string) (User, error) {
	r, err := s.authRead().GetUserByEmail(ctx, email)
	if err != nil {
		return User{}, authErr("get user", err)
	}
	return userFromRow(r), nil
}

func (s *SQLite) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.authRead().ListUsers(ctx)
	if err != nil {
		return nil, authErr("list users", err)
	}
	return usersFromRows(rows), nil
}

func (s *SQLite) DisableUser(ctx context.Context, id string, at int64) error {
	n, err := s.authWrite().DisableUser(ctx, disableParams(id, at))
	if err != nil {
		return authErr("disable user", err)
	}
	if n == 0 {
		// Already disabled, or no such user: only the second is an error.
		_, err := s.GetUser(ctx, id)
		return err
	}
	return nil
}

func (s *SQLite) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	n, err := s.authWrite().SetUserPassword(ctx, sqlitedb.SetUserPasswordParams{PasswordHash: passwordHash, ID: id})
	if err != nil {
		return authErr("set password", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Postgres ---

func (s *Postgres) CreateUser(ctx context.Context, u User) error {
	n, err := s.authQ().InsertUser(ctx, pgdb.InsertUserParams(insertUserParams(u)))
	if err != nil {
		return authErr("create user", err)
	}
	if n == 0 {
		return ErrEmailTaken
	}
	return nil
}

func (s *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	r, err := s.authQ().GetUser(ctx, id)
	if err != nil {
		return User{}, authErr("get user", err)
	}
	return userFromRow(sqlitedb.User(r)), nil
}

func (s *Postgres) GetUserByEmail(ctx context.Context, email string) (User, error) {
	r, err := s.authQ().GetUserByEmail(ctx, email)
	if err != nil {
		return User{}, authErr("get user", err)
	}
	return userFromRow(sqlitedb.User(r)), nil
}

func (s *Postgres) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.authQ().ListUsers(ctx)
	if err != nil {
		return nil, authErr("list users", err)
	}
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		out = append(out, userFromRow(sqlitedb.User(r)))
	}
	return out, nil
}

func (s *Postgres) DisableUser(ctx context.Context, id string, at int64) error {
	n, err := s.authQ().DisableUser(ctx, pgdb.DisableUserParams(disableParams(id, at)))
	if err != nil {
		return authErr("disable user", err)
	}
	if n == 0 {
		_, err := s.GetUser(ctx, id)
		return err
	}
	return nil
}

func (s *Postgres) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	n, err := s.authQ().SetUserPassword(ctx, pgdb.SetUserPasswordParams{PasswordHash: passwordHash, ID: id})
	if err != nil {
		return authErr("set password", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
