//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// engine is one store a server runs on: a SQLite file, or a fresh Postgres database.
type engine struct {
	name string
	// url is BATONDECK_DATABASE_URL; "" is a SQLite file in the server's directory.
	url string
}

// engines is SQLite always, and Postgres when BATONDECK_TEST_POSTGRES_DSN is set: a database made
// for this run and dropped at its end, so every run starts from an empty store.
func engines(t *testing.T) []engine {
	out := []engine{{name: "sqlite"}}
	dsn := os.Getenv("BATONDECK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Log("BATONDECK_TEST_POSTGRES_DSN is unset: the Postgres engine is not run")
		return out
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		t.Fatalf("BATONDECK_TEST_POSTGRES_DSN must be a URL (postgres://…): the suite names its own database in it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("BATONDECK_TEST_POSTGRES_DSN is set and its server does not answer: %v", err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("bk_e2e_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating the run's database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Errorf("dropping %s: %v", name, err)
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	})
	return append(out, engine{name: "postgres", url: withDB(dsn, name)})
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

// identity is one account the suite makes with `user add`, and the labelled tokens it mints for it
// with `token create`.
type identity struct {
	role   string // the suite's name for it
	login  string
	admin  bool     // workspace admin
	labels []string // one token per label; "" is the default label (cli)
}

// identities: a workspace admin (who creates the projects, so is each one's project admin), a
// master, a worker with two agents (two labelled tokens of one identity, plus a third that only the
// disconnect scenario uses), a second worker, a viewer and an outsider who is a member of nothing.
var identities = []identity{
	{role: "admin", login: "admin@e2e.test", admin: true, labels: []string{""}},
	{role: "master", login: "master@e2e.test", labels: []string{"e2e-master"}},
	{role: "worker", login: "worker@e2e.test", labels: []string{"e2e-wa", "e2e-wb", "e2e-disc"}},
	{role: "worker2", login: "second@e2e.test", labels: []string{"e2e-w2"}},
	{role: "viewer", login: "viewer@e2e.test", labels: []string{"e2e-viewer"}},
	{role: "outsider", login: "outsider@e2e.test", labels: []string{"e2e-out"}},
}

// Server is one running binary on one engine.
type Server struct {
	engine string
	base   string // http://127.0.0.1:<port>
	dir    string
	env    []string
	// ids maps a role to its user id, tokens a role or "role/label" to its bearer.
	ids    map[string]string
	logins map[string]string
	tokens map[string]string
	log    *lockedBuffer
	cmd    *exec.Cmd
	cov    *coverage
}

const password = "e2e-password-0123"

// startServer runs migrate, user add and token create for every identity, then serve on a free
// port, and waits for /healthz. The server is stopped at the test's end.
func startServer(t *testing.T, eng engine) *Server {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	db := eng.url
	if db == "" {
		db = filepath.Join(dir, "batondeck-kanban.db")
	}
	s := &Server{engine: eng.name, dir: dir, ids: map[string]string{}, logins: map[string]string{}, tokens: map[string]string{}, log: &lockedBuffer{}, cov: newCoverage()}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BATONDECK_") {
			continue // the caller's own configuration must not leak into the run
		}
		s.env = append(s.env, kv)
	}
	s.env = append(s.env,
		"BATONDECK_DATABASE_URL="+db,
		"BATONDECK_BLOB_DIR="+filepath.Join(dir, "blobs"),
		"BATONDECK_MASTER_KEY="+base64.StdEncoding.EncodeToString(key),
		"XDG_DATA_HOME="+filepath.Join(dir, "xdg"),
	)

	if out := s.cli(t, "", "migrate"); !strings.Contains(out, "schema at version") {
		t.Fatalf("migrate: %q", out)
	}
	userLine := regexp.MustCompile(`^user add: (\S+) (\S+)`)
	tokenLine := regexp.MustCompile(`(?m)^bd_\S+$`)
	for _, id := range identities {
		args := []string{"user", "add"}
		if id.admin {
			args = append(args, "--admin")
		}
		out := s.cli(t, password+"\n", append(args, id.login)...)
		m := userLine.FindStringSubmatch(out)
		if m == nil || m[2] != id.login {
			t.Fatalf("user add %s: %q", id.login, out)
		}
		s.ids[id.role], s.logins[id.role] = m[1], id.login
		for i, label := range id.labels {
			args := []string{"token", "create", "--user", id.login}
			if label != "" {
				args = append(args, "--label", label)
			}
			tok := tokenLine.FindString(s.cli(t, "", args...))
			if tok == "" {
				t.Fatalf("token create for %s %q printed no token", id.login, label)
			}
			if i == 0 {
				s.tokens[id.role] = tok
			}
			s.tokens[id.role+"/"+label] = tok
		}
	}

	port := freePort(t)
	s.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(binPath, "serve")
	cmd.Env = append(append([]string{}, s.env...), fmt.Sprintf("BATONDECK_LISTEN=tcp://127.0.0.1:%d", port))
	cmd.Stdout, cmd.Stderr = s.log, s.log
	cmd.SysProcAttr = serveProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("serve's log (%s):\n%s", eng.name, s.log.String())
		}
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case err := <-exited:
			t.Fatalf("serve exited before /healthz answered: %v\n%s", err, s.log.String())
		default:
		}
		res, err := http.Get(s.base + "/healthz")
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				t.Logf("%s: serve answered /healthz on %s: %s", eng.name, s.base, strings.TrimSpace(string(body)))
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve did not answer /healthz within 60 s\n%s", s.log.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s
}

// cli runs the binary with args against the server's store, stdin given, and returns its stdout;
// a non-zero exit fails the test.
func (s *Server) cli(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = s.env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("batondeck-kanban %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out.String(), errb.String())
	}
	return out.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// rest calls the REST API with token as the bearer and answers the status and the decoded body.
func (s *Server) rest(t *testing.T, token, method, path string, body any) (int, M) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out M
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = M{"_raw": string(raw)}
		}
	}
	return res.StatusCode, out
}

// lockedBuffer is serve's output, safe to read while the process writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fetch sends body to an absolute URL (a signed blob link, the ingest endpoint) with headers and
// answers the status and the raw body.
func fetch(t *testing.T, method, url string, body []byte, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}
