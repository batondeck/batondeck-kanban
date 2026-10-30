package service_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	boundBlobA = "a116e65f9c020dc5c38664593e74e1a5eda18119e716763789af34739f8ffe44"
	boundBlobB = "b116e65f9c020dc5c38664593e74e1a5eda18119e716763789af34739f8ffe44"
)

var boundURLRE = regexp.MustCompile(`/blobs/([0-9a-f]{64})\?exp=([0-9]+)&sig=([0-9a-f]{64})`)

// liveURLs is the blob ids whose download URL in text verifies now: what a browser could fetch.
func liveURLs(s *servicetest.Stack, text string) []string {
	signer, err := blob.NewSigner([]byte("servicetest signing key, 32 bytes or more"))
	if err != nil {
		panic(err)
	}
	var out []string
	for _, m := range boundURLRE.FindAllStringSubmatch(text, -1) {
		if signer.Verify(m[1], blob.Download, m[2], m[3], time.Now()) == nil {
			out = append(out, m[1])
		}
	}
	return out
}

// TestChronicleBlobURLsStayBound (P-244 review B2), on each engine: a write renews only a URL the
// server could still vouch for. mallory holds a download URL for a blob of project A that expired
// a month ago (she was a member then, or someone passed it on); embedding it in a page of her own
// project B, by create, edit or ingest, stores it as written and every read hands it back dead.
// The stored form itself is refused unless the page held its blob. Against that: a URL live when
// written, and an expired one for a blob the page's parent (or a re-ingest's head) already held,
// are stored in the stable form and read live; a row stored before the fix is revived only when
// its URL was live as it was written.
func TestChronicleBlobURLsStayBound(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		ctx := context.Background()
		ch := s.Core.Chronicle()
		alice := domain.Caller{Identity: "alice", TokenScope: "alice"}
		mallory := domain.Caller{Identity: "mallory", TokenScope: "mallory"}
		pa, err := s.Core.Projects().Create(ctx, alice, service.CreateProjectReq{Name: "A"})
		if err != nil {
			t.Fatal(err)
		}
		pb, err := s.Core.Projects().Create(ctx, mallory, service.CreateProjectReq{Name: "B"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ch.ListPages(ctx, mallory, service.ListChroniclePagesReq{ProjectID: pa.ProjectID}); err == nil {
			t.Fatal("mallory reads project A")
		}
		sign := func(id string, exp time.Time) string { return s.Core.SignBlobURL(ctx, id, blob.Download, exp) }
		old := sign(boundBlobA, time.Now().Add(-30*24*time.Hour).Truncate(time.Second))
		stable := strings.TrimPrefix(sign(boundBlobA, time.Unix(0, 0)), s.Core.BlobBase(ctx))
		read := func(who domain.Caller, pid, pageID string) string {
			t.Helper()
			v, err := ch.GetPage(ctx, who, service.GetChroniclePageReq{ProjectID: pid, PageID: pageID})
			if err != nil || v.Head == nil {
				t.Fatalf("get page: %+v, %v", v, err)
			}
			return v.Head.Blocks[0].Text
		}
		stored := func(pid, pageID, versionID string) string {
			t.Helper()
			v, err := s.Store.GetChronicleVersion(ctx, pid, pageID, versionID)
			if err != nil {
				t.Fatal(err)
			}
			return v.Blocks[0].Text
		}
		dead := func(where, pid, pageID, versionID, text string) {
			t.Helper()
			if got := stored(pid, pageID, versionID); got != text {
				t.Errorf("%s: stored %q, want the expired URL as written %q", where, got, text)
			}
			if got := read(mallory, pid, pageID); got != text || len(liveURLs(s, got)) != 0 {
				t.Errorf("%s: read %q, want the expired URL as written, dead", where, got)
			}
		}

		// The reviewer's scenario: create in B with the month-old URL.
		text := "![x](" + old + ")"
		cr, err := ch.CreatePage(ctx, mallory, service.CreateChroniclePageReq{ProjectID: pb.ProjectID, Kind: "adr", Slug: "adr/x", Title: "X",
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: text}}})
		if err != nil {
			t.Fatal(err)
		}
		dead("create", pb.ProjectID, cr.PageID, cr.VersionID, text)

		// An edit whose parent never held the blob: still dead.
		other := "![other](" + sign(boundBlobB, time.Now().Add(time.Hour).Truncate(time.Second)) + ")"
		cr2, err := ch.CreatePage(ctx, mallory, service.CreateChroniclePageReq{ProjectID: pb.ProjectID, Kind: "adr", Slug: "adr/y", Title: "Y",
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: other}}})
		if err != nil {
			t.Fatal(err)
		}
		ed, err := ch.EditPage(ctx, mallory, service.EditChroniclePageReq{ProjectID: pb.ProjectID, PageID: cr2.PageID, ExpectedParentVersionID: cr2.VersionID,
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: text}}})
		if err != nil {
			t.Fatal(err)
		}
		dead("edit", pb.ProjectID, cr2.PageID, ed.VersionID, text)

		// An ingest, first sight and re-ingest over a head that never held it: still dead.
		ingest := func(slug, text string) service.IngestChroniclePageResp {
			t.Helper()
			r, err := ch.IngestPage(ctx, mallory, service.IngestChroniclePageReq{ProjectID: pb.ProjectID, Kind: "adr", Slug: slug, Title: "Z",
				Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: text, Origin: &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(text)}}}})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		in := ingest("adr/z", text)
		dead("ingest", pb.ProjectID, in.PageID, in.VersionID, text)
		ingest("adr/w", other)
		re := ingest("adr/w", text)
		dead("re-ingest", pb.ProjectID, re.PageID, re.VersionID, text)

		// The stored form, which no read returns, is refused where the page did not hold its blob.
		for _, w := range []struct {
			name string
			err  error
		}{
			{"create", func() error {
				_, err := ch.CreatePage(ctx, mallory, service.CreateChroniclePageReq{ProjectID: pb.ProjectID, Kind: "adr", Slug: "adr/s", Title: "S",
					Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: "![s](" + stable + ")"}}})
				return err
			}()},
			{"edit", func() error {
				_, err := ch.EditPage(ctx, mallory, service.EditChroniclePageReq{ProjectID: pb.ProjectID, PageID: cr2.PageID, ExpectedParentVersionID: ed.VersionID,
					Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: "![s](" + stable + ")"}}})
				return err
			}()},
			{"ingest", func() error {
				_, err := ch.IngestPage(ctx, mallory, service.IngestChroniclePageReq{ProjectID: pb.ProjectID, Kind: "adr", Slug: "adr/s2", Title: "S",
					Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: "![s](" + stable + ")"}}})
				return err
			}()},
		} {
			if e, ok := domain.AsBatonDeckError(w.err); !ok || e.Code != domain.CodeValidation {
				t.Errorf("%s with the stored form: %v, want VALIDATION", w.name, w.err)
			}
		}
		if p, _ := ch.ListPages(ctx, mallory, service.ListChroniclePagesReq{ProjectID: pb.ProjectID}); len(p.Pages) != 4 {
			t.Errorf("pages after the refused writes: %d, want the 4 written before (x, y, z, w)", len(p.Pages))
		}

		// alice in A: a URL live when written is stored stable and read live.
		live := "![x](" + sign(boundBlobA, time.Now().Add(10*time.Minute).Truncate(time.Second)) + ")"
		ac, err := ch.CreatePage(ctx, alice, service.CreateChroniclePageReq{ProjectID: pa.ProjectID, Kind: "adr", Slug: "adr/a", Title: "A",
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: live}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := stored(pa.ProjectID, ac.PageID, ac.VersionID); got != "![x]("+stable+")" {
			t.Fatalf("a live URL stored %q, want the stable form", got)
		}
		if got := read(alice, pa.ProjectID, ac.PageID); len(liveURLs(s, got)) != 1 {
			t.Fatalf("a live URL read %q, want it live", got)
		}

		// The editor left open past BlobURLTTL: the URL it read has expired, but the parent holds the
		// blob, so the edit stores it stable and it reads live. An expired URL for a blob the parent
		// does not hold, in the same edit, stays dead.
		expiredB := sign(boundBlobB, time.Now().Add(-time.Minute).Truncate(time.Second))
		late := "![x](" + old + ") ![y](" + expiredB + ")"
		ae, err := ch.EditPage(ctx, alice, service.EditChroniclePageReq{ProjectID: pa.ProjectID, PageID: ac.PageID, ExpectedParentVersionID: ac.VersionID,
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: late}}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := stored(pa.ProjectID, ac.PageID, ae.VersionID), "![x]("+stable+") ![y]("+expiredB+")"; got != want {
			t.Fatalf("a late edit stored %q, want %q", got, want)
		}
		if got := liveURLs(s, read(alice, pa.ProjectID, ac.PageID)); len(got) != 1 || got[0] != boundBlobA {
			t.Fatalf("a late edit reads live %v, want only the held blob", got)
		}
		// The parent holds the stored form, so sending it back is no refusal.
		if _, err := ch.EditPage(ctx, alice, service.EditChroniclePageReq{ProjectID: pa.ProjectID, PageID: ac.PageID, ExpectedParentVersionID: ae.VersionID,
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: "![x](" + stable + ")"}}}); err != nil {
			t.Fatalf("the stored form of a held blob: %v", err)
		}

		// A sweep that re-ingests its file a day later: the head holds the blob, so the expired URL in
		// the file is stored stable, the derived block does not drift and nothing conflicts.
		swept := "Swept ![x](" + live[5:len(live)-1] + ")"
		first, err := ch.IngestPage(ctx, alice, service.IngestChroniclePageReq{ProjectID: pa.ProjectID, Kind: "adr", Slug: "adr/swept", Title: "S",
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: swept, Origin: &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(swept)}}}})
		if err != nil {
			t.Fatal(err)
		}
		dayOld := "Swept ![x](" + old + ")"
		again, err := ch.IngestPage(ctx, alice, service.IngestChroniclePageReq{ProjectID: pa.ProjectID, Kind: "adr", Slug: "adr/swept", Title: "S",
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: dayOld, Origin: &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(dayOld)}}}})
		if err != nil || !again.Unchanged || len(again.Conflicts) != 0 || again.VersionID != first.VersionID {
			t.Fatalf("re-ingest of a held blob's expired URL: %+v, %v, want unchanged", again, err)
		}

		// A head the editor gave the blob while the sweep's base never had it: the re-ingest's expired
		// URL is held by the head, stored stable in the conflict's theirs and read live there.
		plain := "Swept, no image."
		hp, err := ch.IngestPage(ctx, alice, service.IngestChroniclePageReq{ProjectID: pa.ProjectID, Kind: "adr", Slug: "adr/head", Title: "H",
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: plain, Origin: &domain.BlockOrigin{AdrID: "ADR-0002", BlockHash: domain.BlockHash(plain)}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ch.EditPage(ctx, alice, service.EditChroniclePageReq{ProjectID: pa.ProjectID, PageID: hp.PageID, ExpectedParentVersionID: hp.VersionID,
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: "Edited " + live}}}); err != nil {
			t.Fatal(err)
		}
		theirs := "Swept again " + "![x](" + old + ")"
		hc, err := ch.IngestPage(ctx, alice, service.IngestChroniclePageReq{ProjectID: pa.ProjectID, Kind: "adr", Slug: "adr/head", Title: "H",
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: theirs, Origin: &domain.BlockOrigin{AdrID: "ADR-0002", BlockHash: domain.BlockHash(theirs)}}}})
		if err != nil || len(hc.Conflicts) != 1 {
			t.Fatalf("re-ingest over an edited head: %+v, %v, want one conflict", hc, err)
		}
		hv, err := ch.GetPage(ctx, alice, service.GetChroniclePageReq{ProjectID: pa.ProjectID, PageID: hp.PageID})
		if err != nil || hv.Conflicts == nil || len(hv.Conflicts.Items) != 1 || hv.Conflicts.Items[0].Theirs == nil {
			t.Fatalf("the conflict: %+v, %v", hv.Conflicts, err)
		}
		if got := liveURLs(s, hv.Conflicts.Items[0].Theirs.Text); len(got) != 1 {
			t.Fatalf("the conflict's theirs reads %q, want the head-held blob live", hv.Conflicts.Items[0].Theirs.Text)
		}

		// Rows stored before the fix (the store written directly, as the old code did): a URL live as
		// it was written is revived once it expires; one already expired then stays dead.
		exp := time.Now().Add(2 * time.Second).Truncate(time.Second)
		before := "Before: ![x](" + sign(boundBlobA, exp) + ")"
		deadBefore := "Before: ![x](" + old + ")"
		legacy := func(slug, text string) store.UpsertedChroniclePage {
			t.Helper()
			up, err := s.Store.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{ProjectID: pa.ProjectID, Kind: "adr", Slug: slug, Title: "L",
				Blocks: []domain.ChronicleBlock{{ID: "a-1", Text: text}}, Source: domain.SourceHuman, Author: "alice", RefuseExisting: true})
			if err != nil {
				t.Fatal(err)
			}
			return up
		}
		lv, ld := legacy("adr/legacy", before), legacy("adr/legacy-dead", deadBefore)
		time.Sleep(time.Until(exp) + 50*time.Millisecond)
		if got := liveURLs(s, before); len(got) != 0 {
			t.Fatalf("the legacy URL is still live: %v", got)
		}
		if got := read(alice, pa.ProjectID, lv.Page.ID); len(liveURLs(s, got)) != 1 {
			t.Fatalf("a legacy row live when written reads %q, want it re-signed", got)
		}
		vv, err := ch.GetVersion(ctx, alice, service.GetChronicleVersionReq{ProjectID: pa.ProjectID, PageID: lv.Page.ID, VersionID: lv.Page.HeadVersionID})
		if err != nil || len(liveURLs(s, vv.Version.Blocks[0].Text)) != 1 {
			t.Fatalf("the legacy version reads %+v, %v, want it re-signed", vv.Version, err)
		}
		if got := read(alice, pa.ProjectID, ld.Page.ID); got != deadBefore {
			t.Fatalf("a legacy row expired when written reads %q, want it as stored", got)
		}
		// The editor's round trip over that legacy row: the parent holds the blob, so it goes stable.
		rt, err := ch.EditPage(ctx, alice, service.EditChroniclePageReq{ProjectID: pa.ProjectID, PageID: lv.Page.ID, ExpectedParentVersionID: lv.Page.HeadVersionID,
			Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: before}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := stored(pa.ProjectID, lv.Page.ID, rt.VersionID); got != "Before: ![x]("+stable+")" {
			t.Fatalf("an edit over the legacy row stored %q, want the stable form", got)
		}
		t.Logf("%s: mallory's month-old URL stays %q", s.Engine, text)
	})
}
