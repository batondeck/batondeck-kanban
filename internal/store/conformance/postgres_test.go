package conformance_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/conformance"
)

// TestPostgresConformance runs the same suite against a real Postgres, gated by
// BATONDECK_TEST_POSTGRES_DSN. Unset, it skips and says so; set and unreachable, it FAILS: the
// factory's first connection is a t.Fatal, never a skip. Each store the suite asks for is a fresh
// database bk_conf_<n> on that server, dropped first if an earlier run left it.
func TestPostgresConformance(t *testing.T) {
	dsn := os.Getenv("BATONDECK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: the Postgres engine is not judged (set it to a postgres:// URL whose user may CREATE DATABASE)")
	}
	n := 0
	conformance.Run(t, func(t *testing.T) conformance.Migratable {
		n++
		dbName := fmt.Sprintf("bk_conf_%d", n)
		ctx := context.Background()
		admin, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("BATONDECK_TEST_POSTGRES_DSN is set and its server does not answer: %v", err)
		}
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName)
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
			t.Fatal(err)
		}
		admin.Close(ctx)
		s, err := store.OpenPostgres(ctx, rewriteDB(dsn, dbName))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return conformance.WithSeeder(s)
	})
}

// rewriteDB swaps the database name in a postgres URL DSN.
func rewriteDB(dsn, db string) string {
	i := strings.LastIndex(dsn, "/")
	rest := dsn[i+1:]
	if j := strings.Index(rest, "?"); j >= 0 {
		return dsn[:i+1] + db + rest[j:]
	}
	return dsn[:i+1] + db
}
