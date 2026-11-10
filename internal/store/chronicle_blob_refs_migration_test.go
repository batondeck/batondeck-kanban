package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 0014 on a database that already holds Chronicle bodies: the backfill names every blob a
// stored body embeds a download URL of (each once per body, a foreign or malformed id never), a
// body's delete takes its rows with it, and down and up again leave the same rows.
func runChronicleBlobRefsMigration(t *testing.T, db *sql.DB, p *goose.Provider) {
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	sig := "&sig=" + strings.Repeat("f", 64)
	bodies := map[string]string{
		// Go's JSON writes & as &; the id and its ?exp= are as written.
		"CP-1/one":  `[{"id":"a-1","text":"![x](/blobs/` + a + `?exp=0&sig=` + strings.Repeat("f", 64) + `) and ![y](http://h/blobs/` + b + `?exp=0` + sig + `) and again /blobs/` + a + `?exp=99` + sig + `"}]`,
		"CP-1/none": `[{"id":"a-1","text":"no image, /blobs/ alone, /blobs/` + strings.Repeat("A", 64) + `?exp=0 and /blobs/` + a[:63] + `?exp=0"}]`,
		"CP-2/b":    `[{"id":"a-1","text":"/blobs/` + b + `?exp=0` + sig + `"}]`,
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`)
	keys := make([]string, 0, len(bodies))
	for k := range bodies {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for i, k := range keys {
		if isPostgres(db) {
			exec(`INSERT INTO chronicle_blobs (project_id, object_key, body, created_at) VALUES ('P-1', $1, $2, $3)`, k, []byte(bodies[k]), i)
		} else {
			exec(`INSERT INTO chronicle_blobs (project_id, object_key, body, created_at) VALUES ('P-1', ?, ?, ?)`, k, []byte(bodies[k]), i)
		}
	}
	refs := func() []string {
		return sqliteStrings(t, db, `SELECT object_key || ' ' || substr(blob_sha256, 1, 1) FROM chronicle_blob_refs ORDER BY object_key, blob_sha256`)
	}
	want := []string{"CP-1/one a", "CP-1/one b", "CP-2/b b"}
	if _, err := p.UpTo(ctx, 14); err != nil {
		t.Fatalf("0014 up: %v", err)
	}
	if got := refs(); !slices.Equal(got, want) {
		t.Fatalf("0014 up: refs %v, want %v", got, want)
	}
	exec(`DELETE FROM chronicle_blobs WHERE object_key = 'CP-2/b'`)
	if got := refs(); !slices.Equal(got, want[:2]) {
		t.Fatalf("a body's delete left refs %v, want %v", got, want[:2])
	}
	if _, err := p.DownTo(ctx, 6); err != nil {
		t.Fatalf("0014 down: %v", err)
	}
	if _, err := p.UpTo(ctx, 14); err != nil {
		t.Fatalf("0014 up again: %v", err)
	}
	if got := refs(); !slices.Equal(got, want[:2]) {
		t.Fatalf("0014 up again: refs %v, want %v", got, want[:2])
	}
}

func isPostgres(db *sql.DB) bool {
	var v string
	return db.QueryRow(`SELECT version()`).Scan(&v) == nil && strings.Contains(v, "PostgreSQL")
}

func TestChronicleBlobRefsMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "refs.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, err := s.migrator()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.provider()
	if err != nil {
		t.Fatal(err)
	}
	runChronicleBlobRefsMigration(t, s.w, p)
}

func TestChronicleBlobRefsMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0014 is not judged on Postgres")
	}
	ctx := context.Background()
	s, err := OpenPostgres(ctx, postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, db, err := s.migrator()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := m.provider()
	if err != nil {
		t.Fatal(err)
	}
	runChronicleBlobRefsMigration(t, db, p)
}
