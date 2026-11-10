package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("blobs", blobs) }

// blobs is the blob table: put once, get, delete, the refusal to delete a referenced blob, the GC
// list by age and reference, and the refusals.
func blobs(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.GetBlob(ctx, strings.Repeat("0", 64))
		return err
	}
	area(t, "Blobs", newStore, probe, func(t *testing.T) {
		hash := func(c string) string { return strings.Repeat(c, 64) }
		put := func(t *testing.T, s store.Store, sha string) store.Blob {
			t.Helper()
			b, err := s.PutBlob(ctx, sha, 7)
			noErr(t, err)
			time.Sleep(2 * time.Millisecond)
			return b
		}

		t.Run("PutOnceGetAndDelete", func(t *testing.T) {
			s := migrated(t, newStore)
			first := put(t, s, hash("a"))
			if first.SHA256 != hash("a") || first.Bytes != 7 || first.CreatedAt == 0 {
				t.Fatalf("PutBlob = %+v", first)
			}
			again, err := s.PutBlob(ctx, hash("a"), 99)
			noErr(t, err)
			if again != first {
				t.Fatalf("a second put returned %+v, want the first row %+v unchanged", again, first)
			}
			got, err := s.GetBlob(ctx, hash("a"))
			noErr(t, err)
			if got != first {
				t.Fatalf("GetBlob = %+v, want %+v", got, first)
			}
			noErr(t, s.DeleteBlob(ctx, hash("a")))
			_, err = s.GetBlob(ctx, hash("a"))
			wantErr(t, err, domain.CodeNotFound, "Blob")
			wantErr(t, s.DeleteBlob(ctx, hash("a")), domain.CodeNotFound, "Blob")
		})

		t.Run("AReferencedBlobIsNotDeletedNorListedUnreferenced", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			task := seedTasks(t, sd, p, b, "a")[0]
			for _, c := range []string{"a", "b", "c", "d"} {
				put(t, s, hash(c))
			}
			att, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p, TaskID: task, MimeType: "text/plain", Bytes: 7, Kind: store.AttachmentFile, CreatedBy: "alice"})
			noErr(t, err)
			_, err = s.SetAttachmentReady(ctx, p, task, att.ID, hash("a"))
			noErr(t, err)
			_, err = s.WriteMemory(ctx, store.WriteMemoryInput{ProjectID: p, TaskID: task, Scope: store.MemoryTask, Key: "big", Value: json.RawMessage(`null`), BlobSHA256: hash("b"), Author: "alice"})
			noErr(t, err)
			wantErr(t, s.DeleteBlob(ctx, hash("a")), domain.CodeValidation, "")
			wantErr(t, s.DeleteBlob(ctx, hash("b")), domain.CodeValidation, "")
			if _, err := s.GetBlob(ctx, hash("a")); err != nil {
				t.Fatalf("the refused delete removed the blob: %v", err)
			}
			now := time.Now().Add(time.Second).UnixMilli()
			list := func(olderThan int64, limit int) []string {
				t.Helper()
				bs, err := s.ListUnreferencedBlobs(ctx, olderThan, limit)
				noErr(t, err)
				out := make([]string, len(bs))
				for i, b := range bs {
					out[i] = b.SHA256[:1]
				}
				return out
			}
			if got := list(now, 10); !slices.Equal(got, []string{"c", "d"}) {
				t.Fatalf("unreferenced %v, want [c d]", got)
			}
			if got := list(now, 1); !slices.Equal(got, []string{"c"}) {
				t.Fatalf("unreferenced with limit 1 %v, want [c]", got)
			}
			if got := list(1, 10); len(got) != 0 {
				t.Fatalf("unreferenced older than 1 ms %v, want none", got)
			}
		})

		t.Run("AChronicleBodyThatEmbedsABlobHoldsIt", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			put(t, s, hash("a"))
			put(t, s, hash("b"))
			url := func(c string) string { return "/blobs/" + hash(c) + "?exp=0&sig=" + hash("f") }
			u, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{ProjectID: p, Kind: domain.ChronicleADR, Slug: "adr/0001-shot", Title: "Shot",
				Blocks: []domain.ChronicleBlock{{ID: "a-1", Text: "![d](" + url("a") + ")"}}, Source: domain.SourceHuman, Author: "alice"})
			noErr(t, err)
			wantErr(t, s.DeleteBlob(ctx, hash("a")), domain.CodeValidation, "")
			bs, err := s.ListUnreferencedBlobs(ctx, time.Now().Add(time.Second).UnixMilli(), 10)
			noErr(t, err)
			if len(bs) != 1 || bs[0].SHA256 != hash("b") {
				t.Fatalf("unreferenced %+v, want only b: the page's body holds a", bs)
			}
			// An older version still names it after an edit drops it: every version reads it afresh.
			_, err = s.AppendChronicleVersion(ctx, p, u.Page.ID, store.AppendChronicleVersionInput{ExpectedParentVersionID: u.Page.HeadVersionID,
				Blocks: []domain.ChronicleBlock{{ID: "a-1", Text: "no image"}}, Source: domain.SourceHuman, Author: "alice"})
			noErr(t, err)
			wantErr(t, s.DeleteBlob(ctx, hash("a")), domain.CodeValidation, "")
			// The page's deletion lets it go.
			noErr(t, s.DeleteChroniclePage(ctx, p, u.Page.ID, "", "alice", nil))
			noErr(t, s.DeleteBlob(ctx, hash("a")))
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, sha := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 65)} {
				_, err := s.PutBlob(ctx, sha, 1)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.PutBlob(ctx, hash("e"), -1)
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.ListUnreferencedBlobs(ctx, 1, 0)
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
