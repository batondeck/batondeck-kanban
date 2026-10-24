// Package storetest opens migrated engine stores for the tests of the packages above the store
// (internal/bus, internal/retention), which may not name a database driver themselves (ADR-0003
// §C row 2): a fresh SQLite file, and a fresh Postgres database on BATONDECK_TEST_POSTGRES_DSN.
package storetest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// SQLite is a migrated SQLite store on a new file in t's temporary directory, closed at cleanup.
func SQLite(t testing.TB) *store.SQLite {
	t.Helper()
	s, _ := SQLiteFile(t)
	return s
}

// SQLiteFile is SQLite and the path of its file, for a test that reads rows back on a connection of
// its own (internal/cli's row counts).
func SQLiteFile(t testing.TB) (*store.SQLite, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, path
}

var seq atomic.Int64

// Postgres is a migrated Postgres store on a new database of the BATONDECK_TEST_POSTGRES_DSN
// server, dropped at cleanup. Unset, the test skips and says so; set and unreachable, it fails.
func Postgres(t testing.TB) *store.Postgres {
	t.Helper()
	s, _ := PostgresDSN(t)
	return s
}

// PostgresDSN is Postgres and the DSN of its database, for a test that reads rows back on a
// connection of its own.
func PostgresDSN(t testing.TB) (*store.Postgres, string) {
	t.Helper()
	dsn := os.Getenv("BATONDECK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: the Postgres engine is not judged (set it to a postgres:// URL whose user may CREATE DATABASE)")
	}
	ctx := context.Background()
	name := fmt.Sprintf("bk_test_%d_%d", time.Now().UnixNano(), seq.Add(1))
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("BATONDECK_TEST_POSTGRES_DSN is set and its server does not answer: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	db := withDB(dsn, name)
	s, err := store.OpenPostgres(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Errorf("dropping %s: %v", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	})
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s, db
}

// withDB is dsn naming the database db.
func withDB(dsn, db string) string {
	i := strings.LastIndex(dsn, "/")
	rest := dsn[i+1:]
	if j := strings.Index(rest, "?"); j >= 0 {
		return dsn[:i+1] + db + rest[j:]
	}
	return dsn[:i+1] + db
}
