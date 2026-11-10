package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("attachments", attachments) }

// attachments is the attachment area: created pending, made ready on an existing blob, listed oldest
// first, deleted per task, isolated by project, and the refusals.
func attachments(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListAttachments(ctx, "P-probe", "T-1")
		return err
	}
	area(t, "Attachments", newStore, probe, func(t *testing.T) {
		create := func(t *testing.T, s store.Store, p, task, mime string) store.Attachment {
			t.Helper()
			a, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p, TaskID: task, MimeType: mime, Bytes: 10, Kind: store.AttachmentFile, CreatedBy: "alice"})
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return a
		}
		sha := strings.Repeat("ab", 32)

		t.Run("CreatedPendingThenReadyOnItsBlob", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			a, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p, TaskID: task, MimeType: "image/png", Bytes: 42, Kind: store.AttachmentImage, CreatedBy: "alice"})
			noErr(t, err)
			if a.ID == "" || a.TS == 0 || a.Status != store.AttachmentPending || a.BlobSHA256 != "" || a.Bytes != 42 || a.Kind != store.AttachmentImage || a.MimeType != "image/png" || a.CreatedBy != "alice" {
				t.Fatalf("CreateAttachment = %+v", a)
			}
			_, err = s.PutBlob(ctx, sha, 42)
			noErr(t, err)
			ready, err := s.SetAttachmentReady(ctx, p, task, a.ID, sha)
			noErr(t, err)
			if ready.Status != store.AttachmentReady || ready.BlobSHA256 != sha || ready.ID != a.ID {
				t.Fatalf("SetAttachmentReady = %+v", ready)
			}
			_, err = s.SetAttachmentReady(ctx, p, task, a.ID, sha)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("ListIsOldestFirstAndDeleteByTaskIsThatTasks", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			ts := seedTasks(t, sd, p, b, "a", "b")
			first := create(t, s, p, ts[0], "text/plain")
			second := create(t, s, p, ts[0], "text/csv")
			create(t, s, p, ts[1], "text/plain")
			list, err := s.ListAttachments(ctx, p, ts[0])
			noErr(t, err)
			if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
				t.Fatalf("ListAttachments = %+v, want %s then %s", list, first.ID, second.ID)
			}
			n, err := s.DeleteAttachmentsByTask(ctx, p, ts[0])
			noErr(t, err)
			if n != 2 {
				t.Fatalf("deleted %d, want 2", n)
			}
			left, err := s.ListAttachments(ctx, p, ts[1])
			noErr(t, err)
			if len(left) != 1 {
				t.Fatalf("the other task holds %+v, want its one attachment", left)
			}
		})

		// api-http#4: the name attach_file was given is kept, returned by the create, the ready and
		// the list.
		t.Run("TheFileNameIsKept", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			a, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p, TaskID: task, MimeType: "application/pdf", Bytes: 42, Kind: store.AttachmentFile, CreatedBy: "alice", FileName: "spec v2.pdf"})
			noErr(t, err)
			_, err = s.PutBlob(ctx, sha, 42)
			noErr(t, err)
			ready, err := s.SetAttachmentReady(ctx, p, task, a.ID, sha)
			noErr(t, err)
			list, err := s.ListAttachments(ctx, p, task)
			noErr(t, err)
			if a.FileName != "spec v2.pdf" || ready.FileName != "spec v2.pdf" || len(list) != 1 || list[0].FileName != "spec v2.pdf" {
				t.Fatalf("created %q, ready %q, listed %+v: want the name kept", a.FileName, ready.FileName, list)
			}
		})

		// api-http#7: the retention sweep's delete takes the pending attachments created before its
		// cutoff, oldest first and at most its limit, and never a ready one or a younger one.
		t.Run("DeleteStalePendingTakesOnlyOldPendingRows", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			ready := create(t, s, p, task, "text/plain")
			_, err := s.PutBlob(ctx, sha, 10)
			noErr(t, err)
			_, err = s.SetAttachmentReady(ctx, p, task, ready.ID, sha)
			noErr(t, err)
			older := create(t, s, p, task, "text/plain")
			newer := create(t, s, p, task, "text/plain")
			n, err := s.DeleteStalePendingAttachments(ctx, older.TS, 10)
			noErr(t, err)
			if n != 0 {
				t.Fatalf("a cutoff at the older one's own time deleted %d, want 0", n)
			}
			n, err = s.DeleteStalePendingAttachments(ctx, time.Now().Add(time.Hour).UnixMilli(), 1)
			noErr(t, err)
			list, err := s.ListAttachments(ctx, p, task)
			noErr(t, err)
			if n != 1 || len(list) != 2 || list[0].ID != ready.ID || list[1].ID != newer.ID {
				t.Fatalf("deleted %d, left %+v; want the older pending one gone, the ready one and the newer one kept", n, list)
			}
			n, err = s.DeleteStalePendingAttachments(ctx, time.Now().Add(time.Hour).UnixMilli(), 10)
			noErr(t, err)
			list, err = s.ListAttachments(ctx, p, task)
			noErr(t, err)
			if n != 1 || len(list) != 1 || list[0].ID != ready.ID {
				t.Fatalf("deleted %d, left %+v; want only the ready one left", n, list)
			}
			_, err = s.DeleteStalePendingAttachments(ctx, 0, 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("ATaskIDIsScopedByItsProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			pt := seedTasks(t, sd, p, b, "a")[0]
			qt := seedTasks(t, sd, q, qb, "a")[0]
			a := create(t, s, p, pt, "text/plain")
			list, err := s.ListAttachments(ctx, q, qt)
			noErr(t, err)
			if len(list) != 0 {
				t.Fatalf("%s/%s lists %+v: another project's attachment leaked", q, qt, list)
			}
			_, err = s.SetAttachmentReady(ctx, q, qt, a.ID, sha)
			wantErr(t, err, domain.CodeNotFound, "Attachment")
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			_, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p, TaskID: "T-404", MimeType: "text/plain", Kind: store.AttachmentFile, CreatedBy: "alice"})
			wantErr(t, err, domain.CodeNotFound, "Task")
			for _, in := range []store.CreateAttachmentInput{
				{ProjectID: p, TaskID: task, MimeType: "text/plain", Kind: "document", CreatedBy: "alice"},
				{ProjectID: p, TaskID: task, Kind: store.AttachmentFile, CreatedBy: "alice"},
				{ProjectID: p, TaskID: task, MimeType: "text/plain", Kind: store.AttachmentFile},
				{ProjectID: p, TaskID: task, MimeType: "text/plain", Bytes: -1, Kind: store.AttachmentFile, CreatedBy: "alice"},
			} {
				_, err := s.CreateAttachment(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err = s.ListAttachments(ctx, p, "T-404")
			wantErr(t, err, domain.CodeNotFound, "Task")
			_, err = s.SetAttachmentReady(ctx, p, task, "A-missing", sha)
			wantErr(t, err, domain.CodeNotFound, "Attachment")
			a := create(t, s, p, task, "text/plain")
			_, err = s.SetAttachmentReady(ctx, p, task, a.ID, sha)
			wantErr(t, err, domain.CodeNotFound, "Blob")
			list, err := s.ListAttachments(ctx, p, task)
			noErr(t, err)
			if len(list) != 1 || list[0].Status != store.AttachmentPending {
				t.Fatalf("after the refusals %+v, want the one pending attachment", list)
			}
		})
	})
}
