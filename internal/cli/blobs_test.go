package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	token   string
	port    string // the TCP listener's
	sock    string // the Unix socket's path, "" for none
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
	return startBlobsOn(t, st, maxBytes, blobListen{tcp: "tcp://127.0.0.1:0", dial: "127.0.0.1"})
}

// blobListen is where startBlobsOn serves: tcp is the TCP listen entry, dial the host the MCP client
// and base use for it, unix (when set) adds a Unix socket in a short temporary directory, and
// publicURL is serve's public_url.
type blobListen struct {
	tcp, dial, publicURL string
	unix                 bool
}

func startBlobsOn(t *testing.T, st store.Store, maxBytes int64, on blobListen) *blobServer {
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
	entries, sock := []string{on.tcp}, ""
	if on.unix {
		sock = filepath.Join(shortDir(t, "bkb"), "bk.sock")
		entries = append(entries, "unix://"+sock)
	}
	lns, err := listenAll(entries, logger)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(lns[0].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + net.JoinHostPort(on.dial, port)
	dir := t.TempDir()
	media, err := mediaOptions(&core.Config{PublicURL: on.publicURL, Blob: core.Blob{Dir: dir, MaxBytes: maxBytes}}, keys, lns, logger)
	if err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", on.publicURL, logger, keys, media, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serveUntil(serveCtx, lns, a, "x", logger) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	})
	client := sdk.NewClient(&sdk.Implementation{Name: "blobs", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: base + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &blobServer{base: base, token: token, port: port, sock: sock, cs: cs, a: a, media: media, dir: dir,
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

// hostRT is bearerRT sending the Host set last, so one MCP session can be reached at two hosts.
type hostRT struct {
	token string
	mu    sync.Mutex
	host  string
}

func (h *hostRT) set(host string) { h.mu.Lock(); h.host = host; h.mu.Unlock() }

func (h *hostRT) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	host := h.host
	h.mu.Unlock()
	r = r.Clone(r.Context())
	r.Host = host
	r.Header.Set("Authorization", "Bearer "+h.token)
	return http.DefaultTransport.RoundTrip(r)
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
				if !strings.Contains(s.logs.String(), "api: blob PUT "+strconv.Quote(id)+" refused: "+c.reason) {
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

// bff is a REST BFF call as s's account: method on path (under /api), sent to dial with the Host
// header host (dial's own when ""), extra headers set; the status and the JSON body.
func (s *blobServer) bff(t *testing.T, c *http.Client, method, dial, host, path string, body any, header map[string]string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, dial+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, out
}

// downloadURL is the first (oldest) attachment's downloadUrl in a list_attachments answer.
func downloadURL(t *testing.T, list map[string]any) string {
	t.Helper()
	atts, _ := list["attachments"].([]any)
	if len(atts) == 0 {
		t.Fatalf("attachments %v, want one at least", list)
	}
	u, _ := atts[0].(map[string]any)["downloadUrl"].(string)
	if u == "" {
		t.Fatalf("no downloadUrl in %v", list)
	}
	return u
}

// at is rawURL sent to dial (scheme://host:port) instead of its own host: the signature covers the
// blob id, the purpose and the expiry, not the host, so a URL signed for one base is served at any.
func at(t *testing.T, rawURL, dial string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := url.Parse(dial)
	u.Scheme, u.Host = d.Scheme, d.Host
	return u.String()
}

// noWildcard fails for a URL that names an address no client can dial.
func noWildcard(t *testing.T, what, u string) {
	t.Helper()
	if strings.Contains(u, "0.0.0.0") || strings.Contains(u, "[::]") {
		t.Errorf("%s %s names a wildcard address", what, u)
	}
}

// P-337: the signed blob URLs start with the address the client reached the server at, not the
// listener's. On a wildcard listener (the image's tcp://:8080) with public_url unset, MCP's
// attach_file and list_attachments sign at the Host the MCP client sent (localhost:<port>), the BFF
// at the Host its request sent, https with X-Forwarded-Proto: https; a request on the Unix socket,
// which names no usable Host, takes the TCP listener's port at 127.0.0.1. No URL names 0.0.0.0 or
// [::], and each one PUTs and GETs. With public_url set, every URL starts with it whatever the Host.
func TestBlobURLBaseOverTheEngines(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name+"/wildcard", func(t *testing.T) {
			s := startBlobsOn(t, e.open(t), 64, blobListen{tcp: "tcp://:0", dial: "localhost", unix: true})
			loop := "http://127.0.0.1:" + s.port
			local := "http://localhost:" + s.port
			taskID := s.task(t)
			body := []byte("over the wildcard")
			upload := s.attach(t, taskID, len(body))
			noWildcard(t, "MCP uploadUrl", upload)
			if !strings.HasPrefix(upload, local+"/blobs/") {
				t.Fatalf("MCP uploadUrl %s, want it at %s, the Host the MCP client sent", upload, local)
			}
			if code, got := do(t, http.MethodPut, upload, bytes.NewReader(body), int64(len(body))); code != http.StatusOK {
				t.Fatalf("PUT the MCP uploadUrl: %d %s", code, got)
			}
			list, ok := s.tool(t, "list_attachments", map[string]any{"projectId": s.project, "taskId": taskID})
			if !ok {
				t.Fatalf("list_attachments: %v", list)
			}
			download := downloadURL(t, list)
			noWildcard(t, "MCP downloadUrl", download)
			if !strings.HasPrefix(download, local+"/blobs/") {
				t.Fatalf("MCP downloadUrl %s, want it at %s", download, local)
			}
			if code, got := do(t, http.MethodGet, download, nil, 0); code != http.StatusOK || !bytes.Equal(got, body) {
				t.Fatalf("GET the MCP downloadUrl: %d %q, want 200 %q", code, got, body)
			}

			// The base is each POST's, not the initialize's: go-sdk keeps the initialize request's
			// context values on the session, so a tool call that reaches the same session at another
			// host must still be signed at that host (TokenInfo.Extra carries it per POST).
			host := &hostRT{token: s.token}
			host.set("localhost:" + s.port)
			moved, err := sdk.NewClient(&sdk.Implementation{Name: "moved", Version: "1"}, nil).Connect(context.Background(),
				&sdk.StreamableClientTransport{Endpoint: loop + "/mcp", HTTPClient: &http.Client{Transport: host}, MaxRetries: -1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { moved.Close() })
			host.set("127.0.0.1:" + s.port)
			res, err := moved.CallTool(context.Background(), &sdk.CallToolParams{Name: "attach_file", Arguments: map[string]any{
				"projectId": s.project, "taskId": taskID, "fileName": "m.bin", "mimeType": "application/octet-stream", "kind": "file", "bytes": 1}})
			if err != nil || res.IsError {
				t.Fatalf("attach_file at the moved host: %v %+v", err, res)
			}
			if up, _ := res.StructuredContent.(map[string]any)["uploadUrl"].(string); !strings.HasPrefix(up, loop+"/blobs/") {
				t.Errorf("uploadUrl of a tool call sent with Host 127.0.0.1 on a session initialised at localhost: %q, want it at %s", up, loop)
			}

			path := "/api/projects/" + s.project + "/tasks/" + taskID + "/attachments"
			tcp := &http.Client{Timeout: 10 * time.Second}
			for _, c := range []struct {
				name, host, want string
				header           map[string]string
			}{
				{"Host localhost", "localhost:" + s.port, local, nil},
				{"Host 127.0.0.1", "", loop, nil},
				{"X-Forwarded-Proto https", "localhost:" + s.port, "https://localhost:" + s.port, map[string]string{"X-Forwarded-Proto": "https"}},
			} {
				code, list := s.bff(t, tcp, http.MethodGet, loop, c.host, path, nil, c.header)
				if code != http.StatusOK {
					t.Fatalf("%s: BFF list: %d %v", c.name, code, list)
				}
				u := downloadURL(t, list)
				noWildcard(t, c.name+": BFF downloadUrl", u)
				if !strings.HasPrefix(u, c.want+"/blobs/") {
					t.Errorf("%s: BFF downloadUrl %s, want it at %s", c.name, u, c.want)
				}
				if code, got := do(t, http.MethodGet, at(t, u, loop), nil, 0); code != http.StatusOK || !bytes.Equal(got, body) {
					t.Errorf("%s: GET the BFF downloadUrl: %d %q", c.name, code, got)
				}
			}
			code, made := s.bff(t, tcp, http.MethodPost, loop, "localhost:"+s.port, path, map[string]any{"fileName": "g.bin", "mimeType": "application/octet-stream", "kind": "file", "bytes": 3}, nil)
			if code != http.StatusOK && code != http.StatusCreated {
				t.Fatalf("BFF attach: %d %v", code, made)
			}
			if up, _ := made["uploadUrl"].(string); !strings.HasPrefix(up, local+"/blobs/") {
				t.Errorf("BFF uploadUrl %q, want it at %s", up, local)
			} else if code, got := do(t, http.MethodPut, up, strings.NewReader("abc"), 3); code != http.StatusOK {
				t.Errorf("PUT the BFF uploadUrl: %d %s", code, got)
			}

			code, list = s.bff(t, unixHTTP(s.sock), http.MethodGet, "http://x", "", path, nil, nil)
			if code != http.StatusOK {
				t.Fatalf("BFF list over the socket: %d %v", code, list)
			}
			if u := downloadURL(t, list); !strings.HasPrefix(u, loop+"/blobs/") {
				t.Errorf("downloadUrl over the Unix socket %s, want the listener fallback %s", u, loop)
			}
			if want := "signed URLs at the requesting client's host, " + loop + " for one on the Unix socket"; !strings.Contains(s.logs.String(), want) {
				t.Errorf("serve's blob line is not %q in\n%s", want, s.logs.String())
			}
			for _, l := range strings.Split(s.logs.String(), "\n") {
				if strings.Contains(l, "signed URLs at") {
					noWildcard(t, "serve's log line", l)
				}
			}
		})
		t.Run(e.name+"/public_url", func(t *testing.T) {
			const public = "http://board.test:8443"
			s := startBlobsOn(t, e.open(t), 64, blobListen{tcp: "tcp://127.0.0.1:0", dial: "127.0.0.1", publicURL: public})
			taskID := s.task(t)
			body := []byte("at public_url")
			upload := s.attach(t, taskID, len(body))
			if !strings.HasPrefix(upload, public+"/blobs/") {
				t.Fatalf("MCP uploadUrl %s, want it at public_url %s", upload, public)
			}
			if code, got := do(t, http.MethodPut, at(t, upload, s.base), bytes.NewReader(body), int64(len(body))); code != http.StatusOK {
				t.Fatalf("PUT: %d %s", code, got)
			}
			list, ok := s.tool(t, "list_attachments", map[string]any{"projectId": s.project, "taskId": taskID})
			if !ok {
				t.Fatalf("list_attachments: %v", list)
			}
			if u := downloadURL(t, list); !strings.HasPrefix(u, public+"/blobs/") {
				t.Errorf("MCP downloadUrl %s, want it at %s", u, public)
			}
			path := "/api/projects/" + s.project + "/tasks/" + taskID + "/attachments"
			code, list := s.bff(t, &http.Client{Timeout: 10 * time.Second}, http.MethodGet, s.base, "elsewhere.test:"+s.port, path, nil, map[string]string{"X-Forwarded-Proto": "https"})
			if code != http.StatusOK {
				t.Fatalf("BFF list: %d %v", code, list)
			}
			u := downloadURL(t, list)
			if !strings.HasPrefix(u, public+"/blobs/") {
				t.Errorf("BFF downloadUrl with a foreign Host %s, want it at %s", u, public)
			}
			if code, got := do(t, http.MethodGet, at(t, u, s.base), nil, 0); code != http.StatusOK || !bytes.Equal(got, body) {
				t.Errorf("GET: %d %q", code, got)
			}
			if !strings.Contains(s.logs.String(), "signed URLs at "+public+"/blobs/") {
				t.Errorf("serve's blob line does not name public_url in\n%s", s.logs.String())
			}
		})
	}
}

// api-http#1, store-rest#4: serve's retention sweep removes, from the blob directory, a file no blob
// row names that was last written more than retention.BlobGrace ago and an upload's temporary file
// as old, and keeps a younger file with no row.
func TestServesSweepRemovesBlobFilesNoRowNames(t *testing.T) {
	st := storetest.SQLite(t)
	files, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", "", log.New(io.Discard, "", 0), nil, service.MediaOptions{Files: files}, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(context.Background())
	old := time.Now().Add(-2 * time.Hour)
	commit := func(body string, mod time.Time) string {
		t.Helper()
		s, err := files.Stage(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if err := files.Commit(s); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(files.Dir(), s.SHA256[:2], s.SHA256), mod, mod); err != nil {
			t.Fatal(err)
		}
		return s.SHA256
	}
	gone, young := commit("no row, old", old), commit("no row, young", time.Now())
	if _, err := files.Stage(strings.NewReader("cut off")); err != nil {
		t.Fatal(err)
	}
	tmps, _ := filepath.Glob(filepath.Join(files.Dir(), "tmp", "upload-*"))
	if len(tmps) != 1 {
		t.Fatalf("temporary files %v", tmps)
	}
	if err := os.Chtimes(tmps[0], old, old); err != nil {
		t.Fatal(err)
	}
	swept := a.sweep.Sweep(context.Background())
	onDisk := func(sha string) bool {
		_, err := os.Stat(filepath.Join(files.Dir(), sha[:2], sha))
		return err == nil
	}
	left, _ := filepath.Glob(filepath.Join(files.Dir(), "tmp", "upload-*"))
	if onDisk(gone) || !onDisk(young) || len(left) != 0 || swept.BlobFiles != 2 {
		t.Fatalf("after serve's sweep (%+v): the old file with no row on disk %v (want false), the young one %v (want true), temporary files %v (want none)", swept, onDisk(gone), onDisk(young), left)
	}
}
