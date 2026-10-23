package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
		if strings.Join(types, ",") != "attachment.updated by alice,attachment.created by alice" {
			t.Fatalf("attachment events (newest first) %v", types)
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
		list := m.list(t, task.ID)
		if len(list) != 2 || list[1].ID != short.AttachmentID || list[1].Status != "pending" {
			t.Fatalf("after the refusals %+v", list)
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
