package cli

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/migrations"
)

func run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, "1.2.3-test", &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run("version")
	if code != 0 || out != "batondeck-kanban 1.2.3-test\n" {
		t.Fatalf("version: %d %q", code, out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{"token"}, {"user"}, {"token", "nope"}, {"user", "nope"}, {"token", "create"}, {"user", "add"}, {"token", "revoke"}} {
		if code, _, errs := run(args...); code != 2 || !strings.Contains(errs, "usage: batondeck-kanban "+args[0]) {
			t.Fatalf("%v: %d %q", args, code, errs)
		}
	}
	if code, _, _ := run("nope"); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
	if code, _, _ := run(); code != 2 {
		t.Fatalf("no command: %d", code)
	}
}

func TestHealthzNamesBinaryAndVersion(t *testing.T) {
	srv := httptest.NewServer(handler("1.2.3-test"))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "batondeck-kanban 1.2.3-test ok\n" {
		t.Fatalf("healthz: %d %q", resp.StatusCode, body)
	}
	// The SPA answers every other GET, and an unmatched /api path fails like an API.
	for path, want := range map[string]int{"/": 200, "/board/anything": 200, "/api/nothing": 404} {
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("%s: %d, want %d", path, r.StatusCode, want)
		}
	}
}

func TestHealthcheckExitCodes(t *testing.T) {
	srv := httptest.NewServer(handler("x"))
	addr := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("BATONDECK_LISTEN", addr)
	if code, _, errs := run("healthcheck"); code != 0 {
		t.Fatalf("healthcheck against a live server: %d %q", code, errs)
	}
	srv.Close()
	if code, _, _ := run("healthcheck"); code != 1 {
		t.Fatalf("healthcheck with nothing listening: %d, want 1", code)
	}
}

func TestDialAddr(t *testing.T) {
	for in, want := range map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "127.0.0.1:8080",
		"127.0.0.1:9000": "127.0.0.1:9000",
		"example:80":     "example:80",
	} {
		got, err := dialAddr(in)
		if err != nil || got != want {
			t.Fatalf("dialAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := dialAddr("no-port"); err == nil {
		t.Fatal("dialAddr accepted an address with no port")
	}
}

// gooseRows counts the rows of goose's version table in the SQLite file at path.
func gooseRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM goose_db_version").Scan(&n); err != nil {
		t.Fatalf("goose's version table in %s: %v", path, err)
	}
	return n
}

// sqliteMigrations is the number of SQLite migrations the binary embeds.
func sqliteMigrations(t *testing.T) int {
	t.Helper()
	migs, err := fs.Glob(migrations.SQLite, "sqlite/*.sql")
	if err != nil || len(migs) == 0 {
		t.Fatalf("the embedded SQLite migrations: %v, %d files", err, len(migs))
	}
	return len(migs)
}

// migrate applies the embedded migrations to a fresh file, and a second run applies nothing and
// says so.
func TestMigrateAppliesTheSchemaThenNoChange(t *testing.T) {
	n := sqliteMigrations(t)
	path := filepath.Join(t.TempDir(), "sub", "bk.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	code, out, errs := run("migrate")
	if want := fmt.Sprintf("migrate: sqlite: %d applied, schema at version %d\n", n, n); code != 0 || out != want {
		t.Fatalf("migrate: %d %q %q, want %q", code, out, errs, want)
	}
	if rows := gooseRows(t, path); rows != 1+n {
		t.Fatalf("goose_db_version holds %d rows, want %d: version 0 and one per migration", rows, 1+n)
	}
	code, out, errs = run("migrate")
	if want := fmt.Sprintf("migrate: sqlite: no change, schema at version %d\n", n); code != 0 || out != want || gooseRows(t, path) != 1+n {
		t.Fatalf("a second migrate: %d %q %q, %d rows; want %q", code, out, errs, gooseRows(t, path), want)
	}
}

// doctor reports the version the connection answers with, and fails naming the engine when there
// is no connection to ask.
func TestDoctorReadsTheEngineFromTheConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bk.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	if code, out, _ := run("doctor"); code != 1 || !strings.Contains(out, "FAIL store        sqlite: ") {
		t.Fatalf("doctor on a missing SQLite file: %d %q", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("doctor created the file it was asked about: %v", err)
	}
	if code, _, errs := run("migrate"); code != 0 {
		t.Fatalf("migrate: %s", errs)
	}
	code, out, _ := run("doctor")
	if code != 0 || !regexp.MustCompile(`ok   store        sqlite, the connection reports: SQLite 3\.\d+\.\d+\n`).MatchString(out) {
		t.Fatalf("doctor on SQLite: %d %q", code, out)
	}

	t.Setenv("BATONDECK_DATABASE_URL", "postgres://bk:bk@127.0.0.1:1/bk?sslmode=disable&connect_timeout=2")
	if code, out, _ := run("doctor"); code != 1 || !strings.Contains(out, "FAIL store        postgres: ") {
		t.Fatalf("doctor on an unreachable Postgres: %d %q", code, out)
	}

	if dsn := os.Getenv("BATONDECK_TEST_POSTGRES_DSN"); dsn != "" {
		t.Setenv("BATONDECK_DATABASE_URL", dsn)
		code, out, _ := run("doctor")
		if code != 0 || !strings.Contains(out, "ok   store        postgres, the connection reports: PostgreSQL ") {
			t.Fatalf("doctor on Postgres: %d %q", code, out)
		}
	}
}

// serve migrates before it listens: by the time /healthz answers, a fresh SQLite file holds
// every embedded migration.
func TestServeMigratesBeforeListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	path := filepath.Join(t.TempDir(), "fresh.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	t.Setenv("BATONDECK_LISTEN", addr)
	done := make(chan int, 1)
	var errb bytes.Buffer
	go func() { done <- Run([]string{"serve"}, "x", io.Discard, &errb) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited %d before listening: %s", code, errb.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve did not answer /healthz: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n, want := gooseRows(t, path), 1+sqliteMigrations(t); n != want {
		t.Fatalf("goose_db_version holds %d rows once serve listens, want %d", n, want)
	}
	// serve answers SIGINT, which its signal.NotifyContext holds by the time it listens.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exited %d: %s", code, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop on SIGINT")
	}
}

// Two `migrate` runs on one fresh SQLite file at once both succeed, and the schema ends at the
// binary's version: the data-dir lock lets one migrate while the other waits, then finds no change.
// Without it they race in goose's Up ("table … already exists", SQLITE_BUSY). flock locks belong
// to the open file, so two runs in one process contend as two processes do.
func TestFirstMigrationsOnOneFileBothSucceed(t *testing.T) {
	n := sqliteMigrations(t)
	const rounds = 30
	failed := 0
	var first string
	for r := 0; r < rounds; r++ {
		path := filepath.Join(t.TempDir(), "race.db")
		t.Setenv("BATONDECK_DATABASE_URL", path)
		type result struct {
			code      int
			out, errs string
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				code, out, errs := run("migrate")
				results <- result{code, out, errs}
			}()
		}
		close(start)
		for i := 0; i < 2; i++ {
			if res := <-results; res.code != 0 {
				failed++
				if first == "" {
					first = res.errs
				}
			}
		}
		if rows := gooseRows(t, path); rows != 1+n {
			t.Fatalf("round %d: goose_db_version holds %d rows, want %d", r, rows, 1+n)
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent first migrations failed, the first with: %s", failed, 2*rounds, first)
	}
}

// A serve that starts beside another running serve (the data-dir lock already shared) does not
// migrate: on an unmigrated file it refuses, naming the schema as not migrated; on a migrated one it
// joins and answers /healthz.
func TestAServeBesideAnotherDoesNotMigrate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beside.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	running, err := core.AcquireServeLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := running.Share(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	t.Setenv("BATONDECK_LISTEN", addr)
	var errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- Run([]string{"serve"}, "x", io.Discard, &errb) }()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(errb.String(), "it has not been migrated") {
			t.Fatalf("serve beside a running serve, on an unmigrated file: %d %q", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		<-done
		t.Fatalf("serve beside a running serve migrated an unmigrated file and served it: %q", errb.String())
	}
	if err := running.Release(); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run("migrate"); code != 0 {
		t.Fatalf("migrate: %s", errs)
	}
	if running, err = core.AcquireServeLock(dir); err != nil {
		t.Fatal(err)
	}
	defer running.Release()
	if err := running.Share(); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	go func() { done <- Run([]string{"serve"}, "x", io.Discard, &errb) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		select {
		case code := <-done:
			t.Fatalf("serve beside a running serve exited %d before listening: %s", code, errb.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve beside a running serve did not answer /healthz: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 || !strings.Contains(errb.String(), "migrated by the serve already holding the data dir") {
			t.Fatalf("serve beside a running serve: exit %d, log %q", code, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop on SIGINT")
	}
}
