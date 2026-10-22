package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	for _, cmd := range []string{"migrate", "token", "user", "doctor"} {
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
