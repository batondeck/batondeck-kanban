package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// blobServer is serve's app over st with a keyring, a blob directory capped at maxBytes and a
// client of a fresh account that is a member of one project with one board.
type blobServer struct {
	base    string
	cs      *sdk.ClientSession
	a       *app
	media   service.MediaOptions
	dir     string
	caller  domain.Caller
	project string
	board   string
	logs    *lockedLog
}

func startBlobs(t *testing.T, st store.Store, maxBytes int64) *blobServer {
	t.Helper()
	ctx := context.Background()
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: user.ID}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "B", CreatedBy: user.ID}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	keys, err := core.OpenKeyring("", func(k string) (string, bool) { return env, k == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	logger := log.New(logs, "", 0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	media, err := mediaOptions(&core.Config{Blob: core.Blob{Dir: dir, MaxBytes: maxBytes}}, keys, []net.Listener{ln}, logger)
	if err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", "", logger, keys, media, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serveUntil(serveCtx, []net.Listener{ln}, a, "x", logger) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	})
	client := sdk.NewClient(&sdk.Implementation{Name: "blobs", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &blobServer{base: "http://" + ln.Addr().String(), cs: cs, a: a, media: media, dir: dir,
		caller: domain.Caller{Identity: user.ID, TokenScope: user.ID}, project: p.ID, board: b.ID, logs: logs}
}

func (s *blobServer) tool(t *testing.T, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return map[string]any{"error": res.Content[0].(*sdk.TextContent).Text}, false
	}
	return res.StructuredContent.(map[string]any), true
}

func (s *blobServer) task(t *testing.T) string {
	t.Helper()
	r, err := s.a.core.Tasks().Create(context.Background(), s.caller, service.CreateTaskReq{ProjectID: s.project, BoardID: s.board, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return r.Task.ID
}

func (s *blobServer) attach(t *testing.T, taskID string, n int) string {
	t.Helper()
	out, ok := s.tool(t, "attach_file", map[string]any{"projectId": s.project, "taskId": taskID, "fileName": "f.bin", "mimeType": "application/octet-stream", "kind": "file", "bytes": n})
	if !ok {
		t.Fatalf("attach_file: %v", out)
	}
	return out["uploadUrl"].(string)
}

func do(t *testing.T, method, rawURL string, body io.Reader, length int64) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// tamper flips the first hex digit of rawURL's sig.
func tamper(t *testing.T, rawURL string) string {
	t.Helper()
	u, _ := url.Parse(rawURL)
	q := u.Query()
	sig := []byte(q.Get("sig"))
	if sig[0] == '0' {
		sig[0] = '1'
	} else {
		sig[0] = '0'
	}
	q.Set("sig", string(sig))
	u.RawQuery = q.Encode()
	return u.String()
}

// The signed /blobs routes through the composed server and a real MCP client, on both engines:
// attach_file's URL takes the PUT (200); list_attachments shows the attachment ready with a download
// URL whose GET is the bytes put; an expired, a wrong-purpose and a tampered signature are each 403
// with a log line naming the reason; a body one byte over blob.max_bytes is 413 whether it declares
// its length or not, leaving no file; another project's attachments are FORBIDDEN.
func TestBlobRoutesOverTheEngines(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			s := startBlobs(t, e.open(t), 16)
			taskID := s.task(t)
			body := []byte("hello")
			upload := s.attach(t, taskID, len(body))
			if !strings.HasPrefix(upload, s.base+"/blobs/") {
				t.Fatalf("uploadUrl %s is not at %s", upload, s.base)
			}
			u, _ := url.Parse(upload)
			id := strings.TrimPrefix(u.Path, "/blobs/")
			signer := s.media.Signer
			expired := signer.URL(s.base, id, blob.Upload, time.Now().Add(-time.Second))
			wrongPurpose := signer.URL(s.base, id, blob.Download, time.Now().Add(time.Minute))
			for _, c := range []struct{ name, url, reason string }{
				{"tampered", tamper(t, upload), "bad signature"},
				{"wrong purpose", wrongPurpose, "bad signature"},
				{"expired", expired, "expired"},
			} {
				name := c.name
				if code, got := do(t, http.MethodPut, c.url, bytes.NewReader(body), int64(len(body))); code != http.StatusForbidden {
					t.Errorf("%s: %d %s, want 403", name, code, got)
				}
				if !strings.Contains(s.logs.String(), "api: blob PUT "+id+" refused: "+c.reason) {
					t.Errorf("%s: no log line naming %q in\n%s", name, c.reason, s.logs.String())
				}
			}
			if code, got := do(t, http.MethodPut, upload, bytes.NewReader(body), int64(len(body))); code != http.StatusOK {
				t.Fatalf("the valid PUT: %d %s", code, got)
			}
			list, ok := s.tool(t, "list_attachments", map[string]any{"projectId": s.project, "taskId": taskID})
			if !ok {
				t.Fatalf("list_attachments: %v", list)
			}
			atts := list["attachments"].([]any)
			row := atts[0].(map[string]any)
			sum := sha256.Sum256(body)
			if len(atts) != 1 || row["status"] != "ready" || row["sha256"] != hex.EncodeToString(sum[:]) {
				t.Fatalf("list_attachments %v", list)
			}
			download := row["downloadUrl"].(string)
			code, got := do(t, http.MethodGet, download, nil, 0)
			if code != http.StatusOK || !bytes.Equal(got, body) {
				t.Fatalf("GET the download URL: %d %q, want 200 %q", code, got, body)
			}
			du, _ := url.Parse(download)
			if code, _ := do(t, http.MethodGet, signer.URL(s.base, strings.TrimPrefix(du.Path, "/blobs/"), blob.Upload, time.Now().Add(time.Minute)), nil, 0); code != http.StatusForbidden {
				t.Errorf("GET with an upload signature: %d, want 403", code)
			}
			if code, _ := do(t, http.MethodGet, tamper(t, download), nil, 0); code != http.StatusForbidden {
				t.Errorf("GET with a tampered signature: %d, want 403", code)
			}

			big := s.attach(t, taskID, 16)
			over := bytes.Repeat([]byte("x"), 17)
			for name, length := range map[string]int64{"declared": 17, "chunked": -1} {
				if code, got := do(t, http.MethodPut, big, bytes.NewReader(over), length); code != http.StatusRequestEntityTooLarge {
					t.Errorf("one byte over, %s: %d %s, want 413", name, code, got)
				}
			}
			overSum := sha256.Sum256(over)
			overSHA := hex.EncodeToString(overSum[:])
			if _, err := os.Stat(filepath.Join(s.dir, overSHA[:2], overSHA)); err == nil {
				t.Error("the refused body is on disk")
			}
			if left, _ := os.ReadDir(filepath.Join(s.dir, "tmp")); len(left) != 0 {
				t.Errorf("tmp holds %v after the 413s", left)
			}

			other, err := s.a.core.Projects().Create(context.Background(), domain.Caller{Identity: "bob", TokenScope: "bob"}, service.CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			if out, ok := s.tool(t, "list_attachments", map[string]any{"projectId": other.ProjectID, "taskId": taskID}); ok || !strings.Contains(out["error"].(string), "FORBIDDEN") {
				t.Errorf("another project's list_attachments: %v, want FORBIDDEN", out)
			}
		})
	}
}
