package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// mediaEnv is env with a Core over a blob store in a temporary directory, signed URLs at
// http://bk.test and a 64-byte cap; signed false leaves the Signer out (serve with no keyring).
type mediaEnv struct {
	*env
	files  *blob.Store
	signer *blob.Signer
}

func newMediaEnv(t *testing.T, st store.Store, signed bool) *mediaEnv {
	t.Helper()
	e := newEnv(t, st)
	files, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &mediaEnv{env: e, files: files}
	opts := MediaOptions{Files: files, BaseURL: "http://bk.test", MaxBytes: 64}
	if signed {
		if m.signer, err = blob.NewSigner(bytes.Repeat([]byte{9}, 32)); err != nil {
			t.Fatal(err)
		}
		opts.Signer = m.signer
	}
	if err := e.c.Close(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.c = New(st, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1, Media: opts})
	return m
}

func onMediaEngines(t *testing.T, fn func(t *testing.T, m *mediaEnv)) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) { fn(t, newMediaEnv(t, ec.open(t), true)) })
	}
}

// newTask is a task created through the service, in the board's default status.
func (m *mediaEnv) newTask(t *testing.T) store.Task {
	t.Helper()
	r, err := m.c.Tasks().Create(m.ctx, m.alice, CreateTaskReq{ProjectID: m.project, BoardID: m.board.ID, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return m.get(t, r.Task.ID)
}

func (m *mediaEnv) attach(t *testing.T, taskID string, n int64) AttachFileResp {
	t.Helper()
	r, err := m.c.Media().AttachFile(m.ctx, m.alice, AttachFileReq{ProjectID: m.project, TaskID: taskID, FileName: "f.txt", MimeType: "text/plain", Kind: "file", Bytes: n})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// upload is the PUT's service half: body staged, then CompleteUpload on the URL's ref.
func (m *mediaEnv) upload(t *testing.T, uploadURL string, body []byte) error {
	t.Helper()
	u, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := blob.ParseRef(strings.TrimPrefix(u.Path, "/blobs/"))
	if !ok {
		t.Fatalf("upload URL %s names no ref", uploadURL)
	}
	st, err := m.files.Stage(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return m.c.CompleteUpload(m.ctx, ref, st)
}

func (m *mediaEnv) list(t *testing.T, taskID string) []Attachment {
	t.Helper()
	r, err := m.c.Media().ListAttachments(m.ctx, m.alice, ListAttachmentsReq{ProjectID: m.project, TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	return r.Attachments
}

func (m *mediaEnv) onDisk(sha string) bool {
	_, err := os.Stat(filepath.Join(m.files.Dir(), sha[:2], sha))
	return err == nil
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// verifies reports whether rawURL's signature passes for p.
func (m *mediaEnv) verifies(t *testing.T, rawURL string, p blob.Purpose) error {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme+"://"+u.Host != "http://bk.test" {
		t.Fatalf("URL %s is not at the base URL", rawURL)
	}
	return m.signer.Verify(strings.TrimPrefix(u.Path, "/blobs/"), p, u.Query().Get("exp"), u.Query().Get("sig"), time.Now())
}

// attach_file → upload → list_attachments: pending with no download URL, then ready on the uploaded
// bytes' hash with a download URL for that hash; the upload URL is for upload only and the download
// URL for download only; the task moves one version for each, with attachment.created then
// attachment.updated.
func TestAttachUploadListOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		r := m.attach(t, task.ID, 5)
		if r.AttachmentID == "" || r.ExpiresAt == "" {
			t.Fatalf("attach_file = %+v", r)
		}
		if err := m.verifies(t, r.UploadURL, blob.Upload); err != nil {
			t.Fatalf("the upload URL: %v", err)
		}
		if err := m.verifies(t, r.UploadURL, blob.Download); !errors.Is(err, blob.ErrBadSignature) {
			t.Fatalf("the upload URL as a download: %v, want ErrBadSignature", err)
		}
		if got := m.get(t, task.ID).Version; got != task.Version+1 {
			t.Fatalf("version after attach_file %d, want %d", got, task.Version+1)
		}
		pending := m.list(t, task.ID)
		if len(pending) != 1 || pending[0].Status != "pending" || pending[0].DownloadURL != nil || pending[0].SHA256 != nil || pending[0].ID != r.AttachmentID {
			t.Fatalf("before the upload %+v", pending)
		}
		body := []byte("hello")
		if err := m.upload(t, r.UploadURL, body); err != nil {
			t.Fatalf("upload: %v", err)
		}
		ready := m.list(t, task.ID)
		sha := hashOf(body)
		if len(ready) != 1 || ready[0].Status != "ready" || ready[0].SHA256 == nil || *ready[0].SHA256 != sha || ready[0].DownloadURL == nil {
			t.Fatalf("after the upload %+v", ready)
		}
		if a := ready[0]; a.Bytes != 5 || a.CreatedBy != "alice" || a.Kind != "file" || a.MimeType != "text/plain" || a.TaskID != task.ID ||
			a.GCSURI != "blob:"+m.project+"."+task.ID+"."+r.AttachmentID || a.ThumbURI != nil || a.ExtractedText != nil || a.Ts == "" {
			t.Fatalf("the row %+v", a)
		}
		if err := m.verifies(t, *ready[0].DownloadURL, blob.Download); err != nil {
			t.Fatalf("the download URL: %v", err)
		}
		if err := m.verifies(t, *ready[0].DownloadURL, blob.Upload); !errors.Is(err, blob.ErrBadSignature) {
			t.Fatalf("the download URL as an upload: %v, want ErrBadSignature", err)
		}
		f, err := m.files.OpenFile(sha)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 16)
		n, _ := f.Read(got)
		f.Close()
		if !bytes.Equal(got[:n], body) {
			t.Fatalf("on disk %q, want %q", got[:n], body)
		}
		if got := m.get(t, task.ID).Version; got != task.Version+2 {
			t.Fatalf("version after the upload %d, want %d", got, task.Version+2)
		}
		page, err := m.st.ListTaskEvents(m.ctx, m.project, task.ID, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		var types []string
		for _, ev := range page.Events {
			if strings.HasPrefix(string(ev.Type), "attachment.") {
				types = append(types, string(ev.Type)+" by "+ev.Actor)
			}
		}
		slices.Sort(types) // the two events can share a millisecond, and (ts, id) ties on a random id
		if strings.Join(types, ",") != "attachment.created by alice,attachment.updated by alice" {
			t.Fatalf("attachment events %v, want one created and one updated by alice", types)
		}
	})
}

// The upload's refusals: a second upload of a ready attachment is INVALID_TRANSITION, a body that is
// not the declared size VALIDATION (the attachment stays pending and no file is left), a ref to no
// attachment NOT_FOUND; the bytes are not moved by any of them.
func TestUploadRefusalsOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		r := m.attach(t, task.ID, 3)
		if err := m.upload(t, r.UploadURL, []byte("abc")); err != nil {
			t.Fatal(err)
		}
		v := m.get(t, task.ID).Version
		wantCode(t, m.upload(t, r.UploadURL, []byte("xyz")), domain.CodeInvalidTransition)
		if m.onDisk(hashOf([]byte("xyz"))) {
			t.Fatal("a refused second upload left its file")
		}
		short := m.attach(t, task.ID, 4)
		wantCode(t, m.upload(t, short.UploadURL, []byte("abc")), domain.CodeValidation)
		wantCode(t, m.upload(t, short.UploadURL, []byte("abcde")), domain.CodeValidation)
		if m.onDisk(hashOf([]byte("abcde"))) {
			t.Fatal("a refused upload of the wrong size left its file")
		}
		if !m.onDisk(hashOf([]byte("abc"))) {
			t.Fatal("a refused upload of a hash another row holds unlinked that row's file")
		}
		// Found by id: two attachments made in one millisecond list in id order, which is random.
		list := m.list(t, task.ID)
		i := slices.IndexFunc(list, func(a Attachment) bool { return a.ID == short.AttachmentID })
		if len(list) != 2 || i < 0 || list[i].Status != "pending" {
			t.Fatalf("after the refusals %+v, want %s pending", list, short.AttachmentID)
		}
		st, err := m.files.Stage(strings.NewReader("abc"))
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, m.c.CompleteUpload(m.ctx, blob.Ref{ProjectID: m.project, TaskID: task.ID, AttachmentID: "A-missing"}, st), domain.CodeNotFound)
		if got := m.get(t, task.ID).Version; got != v+1 {
			t.Fatalf("version %d after the refusals, want %d (only the second attach_file)", got, v+1)
		}
	})
}

// attach_file and list_attachments refuse another project's caller (FORBIDDEN), a missing task
// (NOT_FOUND), and attach_file a bad kind, mime type or size (VALIDATION), writing nothing.
func TestMediaToolRefusalsOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		other, err := m.st.CreateProject(m.ctx, store.CreateProjectInput{Name: "Q", CreatedBy: "bob"}, store.WriteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = m.c.Media().ListAttachments(m.ctx, m.alice, ListAttachmentsReq{ProjectID: other.ID, TaskID: task.ID})
		wantCode(t, err, domain.CodeForbidden)
		_, err = m.c.Media().AttachFile(m.ctx, m.alice, AttachFileReq{ProjectID: other.ID, TaskID: task.ID, FileName: "f", MimeType: "text/plain", Kind: "file", Bytes: 1})
		wantCode(t, err, domain.CodeForbidden)
		_, err = m.c.Media().ListAttachments(m.ctx, m.alice, ListAttachmentsReq{ProjectID: m.project, TaskID: "T-404"})
		wantCode(t, err, domain.CodeNotFound)
		ok := AttachFileReq{ProjectID: m.project, TaskID: task.ID, FileName: "f", MimeType: "text/plain", Kind: "file", Bytes: 1}
		missing := ok
		missing.TaskID = "T-404"
		_, err = m.c.Media().AttachFile(m.ctx, m.alice, missing)
		wantCode(t, err, domain.CodeNotFound)
		for name, mut := range map[string]func(*AttachFileReq){
			"kind":        func(r *AttachFileReq) { r.Kind = "document" },
			"mime":        func(r *AttachFileReq) { r.MimeType = "application/x-msdownload" },
			"over cap":    func(r *AttachFileReq) { r.Bytes = 65 },
			"negative":    func(r *AttachFileReq) { r.Bytes = -1 },
			"no fileName": func(r *AttachFileReq) { r.FileName = "" },
		} {
			r := ok
			mut(&r)
			_, err := m.c.Media().AttachFile(m.ctx, m.alice, r)
			if e, isBD := domain.AsBatonDeckError(err); !isBD || e.Code != domain.CodeValidation {
				t.Errorf("%s: %v, want VALIDATION", name, err)
			}
		}
		if _, err := m.c.Media().AttachFile(m.ctx, m.alice, func() AttachFileReq { r := ok; r.Bytes = 64; return r }()); err != nil {
			t.Fatalf("an attachment of exactly the cap: %v", err)
		}
		if list := m.list(t, task.ID); len(list) != 1 {
			t.Fatalf("the refusals wrote attachments: %+v", list)
		}
	})
}

// Without a signer (serve found no keyring) attach_file is INTERNAL and a ready attachment lists with
// a null downloadUrl.
func TestMediaWithoutASigner(t *testing.T) {
	m := newMediaEnv(t, engineCases[0].open(t), false)
	task := m.newTask(t)
	_, err := m.c.Media().AttachFile(m.ctx, m.alice, AttachFileReq{ProjectID: m.project, TaskID: task.ID, FileName: "f", MimeType: "text/plain", Kind: "file", Bytes: 1})
	wantCode(t, err, domain.CodeInternal)
	a, err := m.st.CreateAttachment(m.ctx, store.CreateAttachmentInput{ProjectID: m.project, TaskID: task.ID, MimeType: "text/plain", Bytes: 1, Kind: store.AttachmentFile, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sha := hashOf([]byte("x"))
	if _, err := m.st.PutBlob(m.ctx, sha, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.st.SetAttachmentReady(m.ctx, m.project, task.ID, a.ID, sha); err != nil {
		t.Fatal(err)
	}
	if list := m.list(t, task.ID); len(list) != 1 || list[0].Status != "ready" || list[0].DownloadURL != nil {
		t.Fatalf("list without a signer %+v", list)
	}
}

// delete_task's blob GC (CollectBlobs, the default hook with a blob store): deleting a task with two
// attachments removes both blobs' rows and files, except the one another task's attachment still
// holds, which goes when that task does.
func TestDeleteTaskCollectsItsBlobsOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		a, b := m.newTask(t), m.newTask(t)
		shared, own := []byte("shared"), []byte("own")
		for _, up := range []struct {
			task store.Task
			body []byte
		}{{a, shared}, {a, own}, {b, shared}} {
			if err := m.upload(t, m.attach(t, up.task.ID, int64(len(up.body))).UploadURL, up.body); err != nil {
				t.Fatal(err)
			}
		}
		if !m.onDisk(hashOf(shared)) || !m.onDisk(hashOf(own)) {
			t.Fatal("the uploads are not on disk")
		}
		del := func(task store.Task) {
			t.Helper()
			if _, err := m.c.Tasks().Delete(m.ctx, m.alice, DeleteTaskReq{ProjectID: m.project, TaskID: task.ID, Version: m.get(t, task.ID).Version}); err != nil {
				t.Fatal(err)
			}
		}
		del(a)
		if m.onDisk(hashOf(own)) {
			t.Fatal("the deleted task's own blob is still on disk")
		}
		if _, err := m.st.GetBlob(m.ctx, hashOf(own)); err == nil {
			t.Fatal("the deleted task's own blob row is still there")
		}
		if !m.onDisk(hashOf(shared)) {
			t.Fatal("a blob another task still holds was removed")
		}
		del(b)
		if m.onDisk(hashOf(shared)) {
			t.Fatal("the shared blob outlived its last attachment")
		}
		if left, _ := os.ReadDir(filepath.Join(m.files.Dir(), "tmp")); len(left) != 0 {
			t.Fatalf("tmp holds %v", left)
		}
	})
}

// A Chronicle record that embeds an attachment holds its blob: deleting the source task (or a losing
// run) frees the attachment, not the file the record shows. Deleting the record lets the next GC
// collect it.
func TestAChronicleRecordHoldsItsEmbeddedBlobOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		// A page body is capped by blob.max_bytes too: room for one block with a URL.
		if err := m.c.Close(m.ctx); err != nil {
			t.Fatal(err)
		}
		m.c = New(m.st, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1,
			Media: MediaOptions{Files: m.files, BaseURL: "http://bk.test", MaxBytes: 4096, Signer: m.signer}})
		task := m.newTask(t)
		body := []byte("diagram")
		if err := m.upload(t, m.attach(t, task.ID, int64(len(body))).UploadURL, body); err != nil {
			t.Fatal(err)
		}
		atts := m.list(t, task.ID)
		if len(atts) != 1 || atts[0].DownloadURL == nil {
			t.Fatalf("attachments %+v, want one with a download URL", atts)
		}
		page, err := m.c.Chronicle().CreatePage(m.ctx, m.alice, CreateChroniclePageReq{ProjectID: m.project, Kind: "adr", Slug: "adr/0001-shot", Title: "Shot",
			Blocks: []ChronicleBlockInput{{ID: "a-1", Text: "![d](" + *atts[0].DownloadURL + ")"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.c.Tasks().Delete(m.ctx, m.alice, DeleteTaskReq{ProjectID: m.project, TaskID: task.ID, Version: m.get(t, task.ID).Version}); err != nil {
			t.Fatal(err)
		}
		sha := hashOf(body)
		if _, err := m.st.GetBlob(m.ctx, sha); err != nil || !m.onDisk(sha) {
			t.Fatalf("after delete_task the record's blob: row %v, on disk %v; want both kept", err, m.onDisk(sha))
		}
		if left, err := m.st.ListUnreferencedBlobs(m.ctx, time.Now().Add(time.Hour).UnixMilli(), 10); err != nil || len(left) != 0 {
			t.Fatalf("unreferenced blobs %+v %v, want none: retention would collect the record's image", left, err)
		}
		if _, err := m.c.Chronicle().DeletePage(m.ctx, m.alice, DeleteChroniclePageReq{ProjectID: m.project, PageID: page.PageID}); err != nil {
			t.Fatal(err)
		}
		m.c.CollectBlobs(m.ctx, []string{sha})
		if _, err := m.st.GetBlob(m.ctx, sha); err == nil || m.onDisk(sha) {
			t.Fatal("the blob outlived the last record that named it")
		}
	})
}

// Two uploads of one attachment at once, different bytes: one wins, the other is INVALID_TRANSITION,
// and the loser's file is not left on disk.
func TestConcurrentUploadsOneWinnerOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		r := m.attach(t, task.ID, 3)
		bodies := [][]byte{[]byte("one"), []byte("two")}
		errs := make(chan error, 2)
		for _, b := range bodies {
			go func() { errs <- m.upload(t, r.UploadURL, b) }()
		}
		var won, lost int
		for range bodies {
			err := <-errs
			if err == nil {
				won++
				continue
			}
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeInvalidTransition {
				lost++
				continue
			}
			t.Errorf("an upload: %v", err)
		}
		if won != 1 || lost != 1 {
			t.Fatalf("%d won, %d refused, want one of each", won, lost)
		}
		list := m.list(t, task.ID)
		for _, b := range bodies {
			if sha := hashOf(b); m.onDisk(sha) != (*list[0].SHA256 == sha) {
				t.Errorf("%q on disk %v, the winner %s", b, m.onDisk(sha), *list[0].SHA256)
			}
		}
	})
}

// gatedStore holds the first Atomically after arm until release: CompleteUpload's mutation, after
// its file is moved into place and before its row is written.
type gatedStore struct {
	store.Store
	armed            atomic.Bool
	reached, release chan struct{}
}

func (g *gatedStore) arm() {
	g.reached, g.release = make(chan struct{}), make(chan struct{})
	g.armed.Store(true)
}

func (g *gatedStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	if g.armed.CompareAndSwap(true, false) {
		close(g.reached)
		<-g.release
	}
	return g.Store.Atomically(ctx, fn)
}

// N2 (review round 1): the hash lock CompleteUpload holds from the file's move to the row's commit
// is what stops a blob GC of the same bytes, which judged the hash unreferenced, from unlinking the
// file under a row about to say ready. Task A's attachment held H and is gone; an upload of H's bytes
// onto task B is held between its rename and its mutation while CollectBlobs(H) runs: B ends ready
// with H's file on disk, the GC having waited for the lock and then found H referenced.
func TestTheHashLockKeepsAReadyRowsFileOverTheEngines(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			g := &gatedStore{Store: ec.open(t)}
			m := newMediaEnv(t, g, true)
			if err := m.c.Close(m.ctx); err != nil {
				t.Fatal(err)
			}
			opts := MediaOptions{Files: m.files, Signer: m.signer, BaseURL: "http://bk.test", MaxBytes: 64}
			m.c = New(g, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1, Media: opts, BlobGC: func(context.Context, []string) {}})
			body := []byte("same bytes")
			sha := hashOf(body)
			a, b := m.newTask(t), m.newTask(t)
			if err := m.upload(t, m.attach(t, a.ID, int64(len(body))).UploadURL, body); err != nil {
				t.Fatal(err)
			}
			if _, err := m.c.Tasks().Delete(m.ctx, m.alice, DeleteTaskReq{ProjectID: m.project, TaskID: a.ID, Version: m.get(t, a.ID).Version}); err != nil {
				t.Fatal(err)
			}
			up := m.attach(t, b.ID, int64(len(body))).UploadURL
			g.arm()
			uploaded := make(chan error, 1)
			go func() { uploaded <- m.upload(t, up, body) }()
			<-g.reached
			collected := make(chan struct{})
			go func() { m.c.CollectBlobs(m.ctx, []string{sha}); close(collected) }()
			select {
			case <-collected: // no lock held it: the GC ran inside the upload's window
			case <-time.After(200 * time.Millisecond):
			}
			close(g.release)
			if err := <-uploaded; err != nil {
				t.Fatalf("upload: %v", err)
			}
			<-collected
			list := m.list(t, b.ID)
			if len(list) != 1 || list[0].Status != "ready" {
				t.Fatalf("task B %+v, want its attachment ready", list)
			}
			if !m.onDisk(sha) {
				t.Fatal("task B's attachment is ready and its file is gone: the GC unlinked it inside the upload")
			}
		})
	}
}

// cancellingStore cancels a request's context at the first Atomically after arm: CompleteUpload's
// mutation, after its file is moved into place, as net/http does when the client closes the
// connection right after the body.
type cancellingStore struct {
	store.Store
	cancel atomic.Pointer[context.CancelFunc]
}

func (c *cancellingStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	if f := c.cancel.Swap(nil); f != nil {
		(*f)()
	}
	return c.Store.Atomically(ctx, fn)
}

// api-http#1: an upload whose client goes away once its body is in finishes anyway, row and file
// together, instead of failing its mutation on the cancelled context and leaving a file no blob row
// names (which nothing ever removed).
func TestAnUploadWhoseClientLeftAfterTheBodyFinishesOverTheEngines(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			g := &cancellingStore{Store: ec.open(t)}
			m := newMediaEnv(t, g, true)
			body := []byte("the client left")
			sha := hashOf(body)
			task := m.newTask(t)
			u, err := url.Parse(m.attach(t, task.ID, int64(len(body))).UploadURL)
			if err != nil {
				t.Fatal(err)
			}
			ref, _ := blob.ParseRef(strings.TrimPrefix(u.Path, "/blobs/"))
			st, err := m.files.Stage(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(m.ctx)
			defer cancel()
			g.cancel.Store(&cancel)
			err = m.c.CompleteUpload(ctx, ref, st)
			list := m.list(t, task.ID)
			if m.onDisk(sha) && list[0].Status != "ready" {
				t.Fatalf("the file is on disk and the attachment is %s (CompleteUpload: %v): a file no row names", list[0].Status, err)
			}
			if err != nil || list[0].Status != "ready" || !m.onDisk(sha) {
				t.Fatalf("CompleteUpload: %v, attachment %s, on disk %v; want it ready with its file", err, list[0].Status, m.onDisk(sha))
			}
		})
	}
}

// orphan is body committed to the blob store with no row, last written at mod.
func (m *mediaEnv) orphan(t *testing.T, body []byte, mod time.Time) string {
	t.Helper()
	st, err := m.files.Stage(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.files.Commit(st); err != nil {
		t.Fatal(err)
	}
	sha := hashOf(body)
	if err := os.Chtimes(filepath.Join(m.files.Dir(), sha[:2], sha), mod, mod); err != nil {
		t.Fatal(err)
	}
	return sha
}

// api-http#1, store-rest#4: the blob-file sweep removes what no row will ever name, last written
// before its time: a committed file no blob row names, and an upload's temporary file. It keeps a
// ready attachment's file however old, a younger file with no row (an upload between its move and
// its row) and a younger temporary file (an upload still reading). It looks up at most
// blobFileChecks files per call and the next call goes on from there.
func TestSweepBlobFilesRemovesOnlyWhatNoRowNamesOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		now := time.Now()
		old, cut := now.Add(-2*time.Hour), now.Add(-time.Hour)
		task := m.newTask(t)
		kept := []byte("a ready attachment's bytes")
		if err := m.upload(t, m.attach(t, task.ID, int64(len(kept))).UploadURL, kept); err != nil {
			t.Fatal(err)
		}
		keptSHA := hashOf(kept)
		if err := os.Chtimes(filepath.Join(m.files.Dir(), keptSHA[:2], keptSHA), old, old); err != nil {
			t.Fatal(err)
		}
		gone := m.orphan(t, []byte("a file no row names"), old)
		young := m.orphan(t, []byte("an upload between its move and its row"), now)
		for _, body := range []string{"an upload a crash cut off", "an upload still reading"} {
			if _, err := m.files.Stage(strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		}
		tmps, _ := filepath.Glob(filepath.Join(m.files.Dir(), "tmp", "upload-*"))
		if len(tmps) != 2 {
			t.Fatalf("temporary files %v, want 2", tmps)
		}
		if err := os.Chtimes(tmps[0], old, old); err != nil {
			t.Fatal(err)
		}
		if n := m.c.SweepBlobFiles(m.ctx, cut); n != 2 {
			t.Fatalf("removed %d files, want 2 (the old orphan and the old temporary file)", n)
		}
		for sha, want := range map[string]bool{keptSHA: true, gone: false, young: true} {
			if m.onDisk(sha) != want {
				t.Errorf("%s on disk %v, want %v", sha, m.onDisk(sha), want)
			}
		}
		left, _ := filepath.Glob(filepath.Join(m.files.Dir(), "tmp", "upload-*"))
		if len(left) != 1 || left[0] != tmps[1] {
			t.Fatalf("temporary files left %v, want only %s", left, tmps[1])
		}
		if list := m.list(t, task.ID); list[0].Status != "ready" {
			t.Fatalf("the ready attachment is %s", list[0].Status)
		}

		defer func(n int) { blobFileChecks = n }(blobFileChecks)
		blobFileChecks = 2
		// Six files, in hash order alternately named by a row and not, plus kept: no two files with
		// no row are next to each other in hash order, so each call's two lookups find at most one
		// (and the majority guard, tested on its own below, never holds a call back).
		bodies := []string{"one", "two", "three", "four", "five", "six"}
		slices.SortFunc(bodies, func(a, b string) int { return strings.Compare(hashOf([]byte(a)), hashOf([]byte(b))) })
		var three, rows []string
		for i, b := range bodies {
			if i%2 == 0 {
				rows = append(rows, m.named(t, []byte(b), old))
			} else {
				three = append(three, m.orphan(t, []byte(b), old))
			}
		}
		// Seven old files, two per call in hash order from the cursor: four calls go round once.
		var got []int64
		for range 4 {
			got = append(got, m.c.SweepBlobFiles(m.ctx, cut))
		}
		if got[0]+got[1]+got[2]+got[3] != 3 || slices.Max(got) > 1 {
			t.Fatalf("removed %v over four bounded calls, want 3 in all and at most 1 per call", got)
		}
		for _, sha := range three {
			if m.onDisk(sha) {
				t.Errorf("%s still on disk after the bounded calls went round", sha)
			}
		}
		for _, sha := range append(rows, keptSHA) {
			if !m.onDisk(sha) {
				t.Fatalf("%s, which a row names, is gone", sha)
			}
		}
	})
}

// api-http#4 (review): the kept file name is bounded and printable: valid UTF-8, at most 255
// characters (a file name's limit on common file systems), no control characters. Anything else is
// VALIDATION on fileName before an attachment is made; a name with spaces, accents or a slash
// is kept as given.
func TestAttachFileBoundsTheFileNameOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		attach := func(name string) error {
			_, err := m.c.Media().AttachFile(m.ctx, m.alice, AttachFileReq{ProjectID: m.project, TaskID: task.ID, FileName: name, MimeType: "text/plain", Kind: "file", Bytes: 3})
			return err
		}
		for _, bad := range []string{strings.Repeat("a", 256), "line\nbreak.txt", "tab\t.txt", "nul\x00.txt", "bad\xff.txt", "del\x7f.txt"} {
			err := attach(bad)
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation || e.Details["field"] != "fileName" {
				t.Errorf("fileName %q: %v, want VALIDATION on fileName", bad, err)
			}
		}
		if list := m.list(t, task.ID); len(list) != 0 {
			t.Fatalf("a refused name left attachments: %+v", list)
		}
		for _, good := range []string{strings.Repeat("a", 251) + ".txt", strings.Repeat("設", 251) + ".txt", "Résumé (final) v2.txt", "docs/spec.txt"} {
			if err := attach(good); err != nil {
				t.Errorf("fileName %q: %v, want it kept", good, err)
			}
		}
	})
}

// named is body committed to the blob store with a blob row, last written at mod.
func (m *mediaEnv) named(t *testing.T, body []byte, mod time.Time) string {
	t.Helper()
	sha := m.orphan(t, body, mod)
	if _, err := m.st.PutBlob(m.ctx, sha, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	return sha
}

// api-http#1 (review): the blob-file sweep removes a file because no row names it, so a board
// started against a database that is not its own, or before its dump is loaded back, would see every
// file as one to remove. When more than half the files one sweep looks up have no row, it keeps them
// all and logs why; once rows name at least half, the rest go as before.
func TestSweepBlobFilesKeepsEveryFileWhenMostHaveNoRowOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		if err := m.c.Close(m.ctx); err != nil {
			t.Fatal(err)
		}
		logs := &logBuf{}
		m.c = New(m.st, Options{Logger: log.New(logs, "", 0), ToolCallFlush: -1,
			Media: MediaOptions{Files: m.files, Signer: m.signer, BaseURL: "http://bk.test", MaxBytes: 64}})
		t.Cleanup(func() { _ = m.c.Close(context.Background()) })
		now := time.Now()
		old, cut := now.Add(-2*time.Hour), now.Add(-time.Hour)
		var orphans []string
		for _, b := range []string{"one", "two", "three"} {
			orphans = append(orphans, m.orphan(t, []byte(b), old))
		}
		onDisk := func(when string, shas ...string) {
			t.Helper()
			for _, sha := range shas {
				if !m.onDisk(sha) {
					t.Fatalf("%s: %s was removed", when, sha)
				}
			}
		}
		// The database names none of them (empty, or not restored yet).
		if n := m.c.SweepBlobFiles(m.ctx, cut); n != 0 {
			t.Fatalf("an empty database: removed %d files, want 0", n)
		}
		onDisk("an empty database", orphans...)
		if !strings.Contains(logs.String(), "3 of 3 blob files") {
			t.Fatalf("an empty database: the log does not say why the files were kept: %q", logs.String())
		}
		// It names one of four: most still have no row.
		rows := []string{m.named(t, []byte("four"), old)}
		if n := m.c.SweepBlobFiles(m.ctx, cut); n != 0 {
			t.Fatalf("one of four named: removed %d files, want 0", n)
		}
		onDisk("one of four named", append(rows, orphans...)...)
		// It names half: the files no row names go, those it names stay.
		rows = append(rows, m.named(t, []byte("five"), old), m.named(t, []byte("six"), old))
		if n := m.c.SweepBlobFiles(m.ctx, cut); n != 3 {
			t.Fatalf("three of six named: removed %d files, want the 3 no row names", n)
		}
		onDisk("three of six named", rows...)
		for _, sha := range orphans {
			if m.onDisk(sha) {
				t.Errorf("three of six named: %s, which no row names, is still on disk", sha)
			}
		}
	})
}

// api-http#7: attach_file replayed under its idempotency key (as the key invites after a failed
// PUT) answers the same attachment with an upload URL signed at the replay, not the stored URL,
// which lapsed BlobURLTTL after the first call while the key lives a day.
func TestAnAttachReplayedAfterItsURLLapsedAnswersAFreshURLOverTheEngines(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			st := ec.open(t)
			m := newMediaEnv(t, st, true)
			if err := m.c.Close(m.ctx); err != nil {
				t.Fatal(err)
			}
			var at atomic.Int64
			at.Store(time.Now().UnixNano())
			clock := func() time.Time { return time.Unix(0, at.Load()) }
			m.c = New(st, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1, Now: clock,
				Media: MediaOptions{Files: m.files, Signer: m.signer, BaseURL: "http://bk.test", MaxBytes: 64}})
			t.Cleanup(func() { _ = m.c.Close(context.Background()) })
			task := m.newTask(t)
			req := AttachFileReq{ProjectID: m.project, TaskID: task.ID, FileName: "f.txt", MimeType: "text/plain", Kind: "file", Bytes: 3, IdempotencyKey: "retry-me"}
			first, err := m.c.Media().AttachFile(m.ctx, m.alice, req)
			if err != nil {
				t.Fatal(err)
			}
			at.Add(int64(BlobURLTTL + time.Minute))
			second, err := m.c.Media().AttachFile(m.ctx, m.alice, req)
			if err != nil {
				t.Fatal(err)
			}
			if second.AttachmentID != first.AttachmentID || len(m.list(t, task.ID)) != 1 {
				t.Fatalf("the replay answered %s, the first call %s, with %d attachments: want the same one", second.AttachmentID, first.AttachmentID, len(m.list(t, task.ID)))
			}
			check := func(raw string) error {
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				return m.signer.Verify(strings.TrimPrefix(u.Path, "/blobs/"), blob.Upload, u.Query().Get("exp"), u.Query().Get("sig"), clock())
			}
			if err := check(first.UploadURL); !errors.Is(err, blob.ErrExpired) {
				t.Fatalf("the first URL at the replay: %v, want expired (the control)", err)
			}
			if err := check(second.UploadURL); err != nil {
				t.Fatalf("the replay's upload URL at the replay: %v (URL %s, expiresAt %s)", err, second.UploadURL, second.ExpiresAt)
			}
			if exp, _ := time.Parse(time.RFC3339Nano, second.ExpiresAt); !exp.After(clock()) {
				t.Fatalf("the replay's expiresAt %s is not after the replay", second.ExpiresAt)
			}
		})
	}
}

// api-http#4: list_attachments answers the fileName attach_file was given, pending and ready.
func TestListAttachmentsAnswersTheFileNameOverTheEngines(t *testing.T) {
	onMediaEngines(t, func(t *testing.T, m *mediaEnv) {
		task := m.newTask(t)
		body := []byte("pdf")
		r, err := m.c.Media().AttachFile(m.ctx, m.alice, AttachFileReq{ProjectID: m.project, TaskID: task.ID, FileName: "spec v2.pdf", MimeType: "application/pdf", Kind: "file", Bytes: int64(len(body))})
		if err != nil {
			t.Fatal(err)
		}
		if list := m.list(t, task.ID); len(list) != 1 || list[0].FileName != "spec v2.pdf" || list[0].Status != "pending" {
			t.Fatalf("pending: %+v", list)
		}
		if err := m.upload(t, r.UploadURL, body); err != nil {
			t.Fatal(err)
		}
		if list := m.list(t, task.ID); len(list) != 1 || list[0].FileName != "spec v2.pdf" || list[0].Status != "ready" {
			t.Fatalf("ready: %+v", list)
		}
		// get_task_context's attachments section (an agent's one-call briefing) names it too.
		cx, err := m.c.Context().GetTaskContext(m.ctx, m.alice, GetTaskContextReq{ProjectID: m.project, TaskID: task.ID, Include: []ContextSection{"attachments"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cx.Attachments) != 1 {
			t.Fatalf("get_task_context attachments: %+v", cx.Attachments)
		}
		raw, err := json.Marshal(cx.Attachments[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"fileName":"spec v2.pdf"`) {
			t.Fatalf("get_task_context attachment does not name the file: %s", raw)
		}
	})
}
