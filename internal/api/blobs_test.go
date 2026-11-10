package api

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
)

// uploads is an Uploads that discards the staged body and counts the calls.
type uploads struct {
	mu sync.Mutex
	n  int
}

func (u *uploads) CompleteUpload(_ context.Context, _ blob.Ref, st *blob.Staged) error {
	st.Discard()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.n++
	return nil
}

func (u *uploads) calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.n
}

type blobRig struct {
	srv    *httptest.Server
	signer *blob.Signer
	files  *blob.Store
	dir    string
	up     *uploads
	logs   *bytes.Buffer
	logMu  *sync.Mutex
}

func newBlobRig(t *testing.T) *blobRig {
	t.Helper()
	signer, err := blob.NewSigner(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := blob.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &blobRig{signer: signer, files: files, dir: dir, up: &uploads{}, logs: &bytes.Buffer{}, logMu: &sync.Mutex{}}
	routes := &BlobRoutes{Signer: signer, Files: files, MaxBytes: 1 << 20, Uploads: r.up, Logger: log.New(lockedWriter{r.logMu, r.logs}, "", 0)}
	mux := http.NewServeMux()
	mux.Handle("PUT /blobs/{id}", routes.Upload())
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (r *blobRig) log() string {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	return r.logs.String()
}

func (r *blobRig) tmpFiles(t *testing.T) int {
	t.Helper()
	left, err := os.ReadDir(filepath.Join(r.dir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(left)
}

// slowPUT opens a PUT of url declaring 100 bytes and sends 10 of them, leaving the body open.
func (r *blobRig) slowPUT(t *testing.T, rawURL string) net.Conn {
	t.Helper()
	u, _ := url.Parse(rawURL)
	c, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := io.WriteString(c, "PUT "+u.RequestURI()+" HTTP/1.1\r\nHost: "+u.Host+"\r\nContent-Length: 100\r\n\r\n"+strings.Repeat("x", 10)); err != nil {
		t.Fatal(err)
	}
	return c
}

func readStatus(t *testing.T, c net.Conn, within time.Duration) (int, string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(within))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("no response within %v: %v", within, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (r *blobRig) waitStaging(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); r.tmpFiles(t) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the slow PUT never reached staging")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// N1 (review round 1): a body still arriving when its URL expires is cut off at the expiry by the
// connection's read deadline: 403, a log line, no temp file, and the service is never called.
func TestUploadBodyIsCutOffAtItsURLsExpiry(t *testing.T) {
	r := newBlobRig(t)
	c := r.slowPUT(t, r.signer.URL(r.srv.URL, "P-1.T-1.A-1", blob.Upload, time.Now().Add(2*time.Second)))
	r.waitStaging(t)
	code, body := readStatus(t, c, 5*time.Second)
	if code != http.StatusForbidden || !strings.Contains(body, "expired before the body arrived") {
		t.Fatalf("a body past its expiry: %d %s, want 403", code, body)
	}
	if n := r.tmpFiles(t); n != 0 {
		t.Fatalf("%d temp files left", n)
	}
	if r.up.calls() != 0 || !strings.Contains(r.log(), "api: blob PUT P-1.T-1.A-1 refused: expired mid-body") {
		t.Fatalf("calls %d, log %q", r.up.calls(), r.log())
	}
}

// N1 (review round 1): while one PUT reads an id's body, a second PUT of the same id is 409 and
// stages nothing; another id is not held up, and once the first finishes the id takes a PUT again.
func TestASecondConcurrentUploadOfOneIDIsRefused(t *testing.T) {
	r := newBlobRig(t)
	same := r.signer.URL(r.srv.URL, "P-1.T-1.A-1", blob.Upload, time.Now().Add(time.Minute))
	c := r.slowPUT(t, same)
	r.waitStaging(t)
	put := func(rawURL string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, rawURL, strings.NewReader("abc"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put(same); code != http.StatusConflict {
		t.Fatalf("a second concurrent PUT of the same id: %d, want 409", code)
	}
	if n := r.tmpFiles(t); n != 1 {
		t.Fatalf("%d temp files, want only the first PUT's", n)
	}
	if code := put(r.signer.URL(r.srv.URL, "P-1.T-1.A-2", blob.Upload, time.Now().Add(time.Minute))); code != http.StatusOK {
		t.Fatalf("another id during the slow PUT: %d, want 200", code)
	}
	if _, err := io.WriteString(c, strings.Repeat("x", 90)); err != nil {
		t.Fatal(err)
	}
	if code, body := readStatus(t, c, 5*time.Second); code != http.StatusOK {
		t.Fatalf("the slow PUT, finished: %d %s", code, body)
	}
	if code := put(same); code != http.StatusOK {
		t.Fatalf("the id after its upload finished: %d, want 200 (released)", code)
	}
	if r.up.calls() != 3 {
		t.Fatalf("CompleteUpload called %d times, want 3", r.up.calls())
	}
}

// api-http#8: an id is logged before any credential passes, so a refusal quotes it and cuts it to 80
// runes, as the hook routes do: a newline in the id cannot forge a second log line, and a long id
// cannot flood the log.
func TestABlobRefusalLogsItsIDQuotedAndCut(t *testing.T) {
	r := newBlobRig(t)
	forged := "x\n2026/11/10 03:00:00 auth: admin@example.com signed in from 10.0.0.9"
	long := strings.Repeat("y", 500)
	for _, id := range []string{forged, long} {
		req, _ := http.NewRequest(http.MethodPut, r.srv.URL+"/blobs/"+url.PathEscape(id)+"?exp=1&sig=00", strings.NewReader("abc"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("PUT %q: %d, want 403", id, resp.StatusCode)
		}
	}
	lines := strings.Split(strings.TrimSuffix(r.log(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two refusals wrote %d log lines, want 2:\n%s", len(lines), r.log())
	}
	if want := `api: blob PUT "x\n2026/11/10 03:00:00 auth: admin@example.com signed in from 10.0.0.9" refused: bad signature`; lines[0] != want {
		t.Fatalf("log line %q, want %q", lines[0], want)
	}
	if want := `api: blob PUT "` + strings.Repeat("y", 80) + `…" refused: bad signature`; lines[1] != want {
		t.Fatalf("log line %q, want %q", lines[1], want)
	}
}

// A body that would leave the blob volume under its reserve is 507 QUOTA_EXCEEDED, with a log line,
// no temp file, and the service never called: uploads cannot fill the disk the database is on.
func TestUploadRefusedUnderTheVolumesReserve(t *testing.T) {
	r := newBlobRig(t)
	r.files.Reserve = 1 << 62 // more than any volume holds
	id := blob.Ref{ProjectID: "P-1", TaskID: "T-1", AttachmentID: "A-1"}.String()
	put := func() (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPut, r.signer.URL(r.srv.URL, id, blob.Upload, time.Now().Add(time.Minute)), strings.NewReader("hello"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, body := put()
	if resp.StatusCode != http.StatusInsufficientStorage || !strings.Contains(body, `"QUOTA_EXCEEDED"`) {
		t.Fatalf("status %d body %s, want 507 QUOTA_EXCEEDED", resp.StatusCode, body)
	}
	if n := r.tmpFiles(t); n != 0 || r.up.calls() != 0 {
		t.Fatalf("%d temp files, %d service calls, want none", n, r.up.calls())
	}
	if !strings.Contains(r.log(), "reserve") {
		t.Fatalf("no log line names the reserve: %q", r.log())
	}
	t.Run("Control", func(t *testing.T) {
		r.files.Reserve = 0
		if resp, body := put(); resp.StatusCode != http.StatusOK || r.up.calls() != 1 {
			t.Fatalf("status %d body %s, %d service calls, want 200 and 1", resp.StatusCode, body, r.up.calls())
		}
	})
}
