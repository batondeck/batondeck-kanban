package cli

import (
	"bytes"
	"database/sql"
	"io"
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

func TestUnbackedCommandsExitTwo(t *testing.T) {
	for _, cmd := range []string{"token", "user"} {
		code, _, errs := run(cmd)
		if code != 2 || errs != cmd+": not implemented in this release\n" {
			t.Fatalf("%s: %d %q", cmd, code, errs)
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

func TestMigrateWithNoMigrationsMakesTheVersionTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "bk.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	code, out, errs := run("migrate")
	if code != 0 || out != "migrate: sqlite: no migrations in this binary; goose's version table is in place\n" {
		t.Fatalf("migrate: %d %q %q", code, out, errs)
	}
	if n := gooseRows(t, path); n != 1 {
		t.Fatalf("goose_db_version holds %d rows, want its version-0 row", n)
	}
	if code, out, errs := run("migrate"); code != 0 || gooseRows(t, path) != 1 {
		t.Fatalf("a second migrate: %d %q %q, %d rows", code, out, errs, gooseRows(t, path))
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
// goose's version table.
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
	if n := gooseRows(t, path); n != 1 {
		t.Fatalf("goose_db_version holds %d rows once serve listens, want 1", n)
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
