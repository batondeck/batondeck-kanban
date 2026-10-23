package conformance

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("chronicle", chronicle) }

func cblock(id, text string) domain.ChronicleBlock {
	return domain.ChronicleBlock{ID: id, Text: text}
}

func pageObjects(t *testing.T, s store.Store, projectID, pageID string) []string {
	t.Helper()
	keys, err := s.ListChronicleObjects(context.Background(), projectID)
	noErr(t, err)
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(k, pageID+"/") {
			out = append(out, k)
		}
	}
	return out
}

// chronicle is the Chronicle area: the eleven methods, every declared error, the version chain and
// its STALE, the bodies a delete leaves (none), and the per-project keying.
func chronicle(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.GetChroniclePage(ctx, "P-probe", "CP-probe")
		return err
	}
	area(t, "Chronicle", newStore, probe, func(t *testing.T) {
		upsert := func(t *testing.T, s store.Store, p, slug string, src domain.ChronicleSource, blocks ...domain.ChronicleBlock) store.UpsertedChroniclePage {
			t.Helper()
			u, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleADR, Slug: slug, Title: "Title " + slug, Blocks: blocks, Source: src, Author: "alice",
			})
			noErr(t, err)
			return u
		}
		appendV := func(s store.Store, p, page, parent string, blocks ...domain.ChronicleBlock) (store.ChronicleVersion, error) {
			return s.AppendChronicleVersion(ctx, p, page, store.AppendChronicleVersionInput{
				ExpectedParentVersionID: parent, Blocks: blocks, Source: domain.SourceHuman, Author: "bob",
			})
		}

		t.Run("UpsertCreatesThePageAndItsFirstVersion", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-a", domain.SourceHuman, cblock("a", "one"))
			if !u.Created || u.Page.ID != domain.ChroniclePageID("adr/0001-a") || u.Page.ProjectID != p || u.Page.Kind != domain.ChronicleADR {
				t.Fatalf("UpsertChroniclePage = %+v", u)
			}
			if u.Page.UpdatedBy == nil || u.Page.UpdatedBy.Author != "alice" || u.Page.Conflict != nil || u.Page.Topics == nil || u.Page.SupersededBy == nil {
				t.Fatalf("a new page = %+v, want updatedBy alice, no conflict, [] topics and supersededBy", u.Page)
			}
			got, err := s.GetChroniclePage(ctx, p, u.Page.ID)
			noErr(t, err)
			if got.HeadVersionID != u.Page.HeadVersionID || got.Slug != "adr/0001-a" {
				t.Fatalf("GetChroniclePage = %+v, want %+v", got, u.Page)
			}
			v, err := s.GetChronicleVersion(ctx, p, u.Page.ID, u.Page.HeadVersionID)
			noErr(t, err)
			if v.ParentVersionID != nil || v.BlockCount != 1 || len(v.Blocks) != 1 || v.Blocks[0].State != domain.BlockAuthored || v.Blocks[0].Citations == nil {
				t.Fatalf("the first version = %+v", v)
			}
			// A human create's merge base is the empty list, never absent.
			base, ok, err := s.GetChronicleBase(ctx, p, u.Page.ID)
			noErr(t, err)
			if !ok || len(base) != 0 {
				t.Fatalf("a human create's base = %v %v, want [] true", base, ok)
			}
			// A sweep's is its blocks, and its provenance is computed, never trusted.
			hash := domain.BlockHash("derived")
			sw, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleADR, Slug: "adr/0002-b", Title: "b", Source: domain.SourceSweep, Author: "sweeper",
				Blocks: []domain.ChronicleBlock{{ID: "d1", State: domain.BlockAuthored, Text: "derived", Origin: &domain.BlockOrigin{AdrID: "ADR-0002", BlockHash: hash}}},
			})
			noErr(t, err)
			base, ok, err = s.GetChronicleBase(ctx, p, sw.Page.ID)
			noErr(t, err)
			if !ok || len(base) != 1 || base[0].State != domain.BlockDerived {
				t.Fatalf("a sweep's base = %+v %v, want its derived block", base, ok)
			}
			_, ok, err = s.GetChronicleConflictTheirs(ctx, p, sw.Page.ID)
			noErr(t, err)
			if ok {
				t.Fatal("a new page carries a conflict")
			}
		})

		t.Run("UpsertOfAnExistingSlugRefreshesMetadataNeverTheHead", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			full := domain.CoverageFull
			first, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleADR, Slug: "adr/0001-a", Title: "old", Blocks: []domain.ChronicleBlock{cblock("a", "x")},
				Source: domain.SourceSweep, Author: "alice", Record: domain.ChronicleRecordFieldsInput{Topics: []string{"auth"}, CoverageSet: true, Coverage: &full},
			})
			noErr(t, err)
			again, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleTopic, Slug: "adr/0001-a", Title: "new", Blocks: []domain.ChronicleBlock{cblock("a", "changed")},
				Source: domain.SourceSweep, Author: "alice",
			})
			noErr(t, err)
			if again.Created || again.Page.Title != "new" || again.Page.Kind != domain.ChronicleTopic || again.Page.HeadVersionID != first.Page.HeadVersionID {
				t.Fatalf("the refresh = %+v, want new title and kind on the same head", again)
			}
			if !slices.Equal(again.Page.Topics, []string{"auth"}) || again.Page.Coverage == nil {
				t.Fatalf("absent record fields were cleared: %+v", again.Page)
			}
			cleared, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleTopic, Slug: "adr/0001-a", Title: "new", Source: domain.SourceSweep, Author: "alice",
				Record: domain.ChronicleRecordFieldsInput{Topics: []string{}, CoverageSet: true},
			})
			noErr(t, err)
			got, err := s.GetChroniclePage(ctx, p, first.Page.ID)
			noErr(t, err)
			if len(cleared.Page.Topics) != 0 || got.Coverage != nil || len(got.Topics) != 0 {
				t.Fatalf("empty record fields did not clear: %+v / %+v", cleared.Page, got)
			}
			list, err := s.ListChronicleVersions(ctx, p, first.Page.ID, "", 0)
			noErr(t, err)
			if len(list.Versions) != 1 {
				t.Fatalf("a refresh wrote %d versions, want the 1 it had", len(list.Versions))
			}
		})

		t.Run("RefuseExistingIsValidationAndChangesNothing", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			first := upsert(t, s, p, "topic/x", domain.SourceHuman)
			_, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{
				ProjectID: p, Kind: domain.ChronicleTopic, Slug: "topic/x", Title: "someone else", Source: domain.SourceHuman, Author: "eve", RefuseExisting: true,
			})
			e := wantErr(t, err, domain.CodeValidation, "")
			if e.Details["pageId"] != first.Page.ID {
				t.Fatalf("the refusal names %v, want pageId %s", e.Details, first.Page.ID)
			}
			got, err := s.GetChroniclePage(ctx, p, first.Page.ID)
			noErr(t, err)
			if got.Title != first.Page.Title || got.Kind != domain.ChronicleADR {
				t.Fatalf("the refused create renamed the page: %+v", got)
			}
		})

		t.Run("AppendMovesTheHeadOnlyFromTheHeadItRead", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-a", domain.SourceHuman, cblock("a", "v1"))
			v2, err := appendV(s, p, u.Page.ID, u.Page.HeadVersionID, cblock("a", "v2"))
			noErr(t, err)
			if v2.ParentVersionID == nil || *v2.ParentVersionID != u.Page.HeadVersionID || len(v2.Blocks) != 1 || v2.Blocks[0].Text != "v2" {
				t.Fatalf("AppendChronicleVersion = %+v", v2)
			}
			time.Sleep(2 * time.Millisecond)
			v3, err := appendV(s, p, u.Page.ID, v2.ID, cblock("a", "v3"))
			noErr(t, err)
			// A writer still holding v2 (or v1) is STALE, names both heads, and writes nothing.
			_, err = appendV(s, p, u.Page.ID, v2.ID, cblock("a", "lost"))
			e := wantErr(t, err, domain.CodeStale, "")
			if e.Details["expectedVersionId"] != v2.ID || e.Details["actualVersionId"] != v3.ID || !e.Retryable {
				t.Fatalf("STALE = %+v, want expected %s actual %s, retryable", e, v2.ID, v3.ID)
			}
			got, err := s.GetChroniclePage(ctx, p, u.Page.ID)
			noErr(t, err)
			if got.HeadVersionID != v3.ID || got.UpdatedBy == nil || got.UpdatedBy.Author != "bob" {
				t.Fatalf("the head = %+v, want %s by bob", got, v3.ID)
			}
			list, err := s.ListChronicleVersions(ctx, p, u.Page.ID, "", 0)
			noErr(t, err)
			ids := []string{}
			for _, v := range list.Versions {
				ids = append(ids, v.ID)
				if v.Blocks != nil {
					t.Fatal("a version list loaded a body")
				}
			}
			if !slices.Equal(ids, []string{v3.ID, v2.ID, u.Page.HeadVersionID}) || list.NextCursor != "" {
				t.Fatalf("versions = %v (next %q), want three, newest first", ids, list.NextCursor)
			}
			page1, err := s.ListChronicleVersions(ctx, p, u.Page.ID, "", 2)
			noErr(t, err)
			page2, err := s.ListChronicleVersions(ctx, p, u.Page.ID, page1.NextCursor, 2)
			noErr(t, err)
			if len(page1.Versions) != 2 || page1.NextCursor == "" || len(page2.Versions) != 1 || page2.Versions[0].ID != u.Page.HeadVersionID || page2.NextCursor != "" {
				t.Fatalf("paging by 2 = %+v / %+v", page1, page2)
			}
		})

		t.Run("RacingAppendsFromOneHeadLandOne", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-race", domain.SourceHuman, cblock("a", "v1"))
			// 32: under Postgres READ COMMITTED a head move without its head condition let 2 to 6 racers
			// land in 3 of 20 runs at 32 racers, and in none of 3 at 8.
			const n = 32
			errs := make(chan error, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := appendV(s, p, u.Page.ID, u.Page.HeadVersionID, cblock("a", "racer "+strconv.Itoa(i)))
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			won := 0
			for err := range errs {
				if err == nil {
					won++
					continue
				}
				if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeStale {
					t.Fatalf("a racer lost with %v, want STALE", err)
				}
			}
			list, err := s.ListChronicleVersions(ctx, p, u.Page.ID, "", 0)
			noErr(t, err)
			if won != 1 || len(list.Versions) != 2 {
				t.Fatalf("%d of %d racers from one head landed, %d versions: want 1 winner and 2 versions", won, n, len(list.Versions))
			}
		})

		// The deterministic guard of the head condition: A moves the head inside a transaction it holds
		// open; B appends from the same head meanwhile; A commits; B must be STALE. Under Postgres READ
		// COMMITTED both pass the head pre-check (neither sees the other's uncommitted head), so only
		// the UPDATE's head_version_id = expected refuses B. The 300 ms is how long B gets to reach its
		// UPDATE and wait on A's row lock before A is released: B arriving later reads A's committed
		// head at the pre-check and is STALE without exercising the clause, so do not shorten it.
		t.Run("AnAppendRacingAHeldHeadMoveIsStale", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-held", domain.SourceHuman, cblock("a", "v1"))
			moved, release := make(chan struct{}), make(chan struct{})
			aDone, bDone := make(chan error, 1), make(chan error, 1)
			go func() {
				aDone <- s.Atomically(ctx, func(tx store.Store) error {
					if _, err := appendV(tx, p, u.Page.ID, u.Page.HeadVersionID, cblock("a", "A")); err != nil {
						close(moved)
						return err
					}
					close(moved)
					<-release
					return nil
				})
			}()
			<-moved
			go func() {
				_, err := appendV(s, p, u.Page.ID, u.Page.HeadVersionID, cblock("a", "B"))
				bDone <- err
			}()
			time.Sleep(300 * time.Millisecond)
			close(release)
			noErr(t, <-aDone)
			err := <-bDone
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeStale {
				t.Fatalf("B, racing A's held head move: %v, want STALE", err)
			}
			list, err := s.ListChronicleVersions(ctx, p, u.Page.ID, "", 0)
			noErr(t, err)
			if len(list.Versions) != 2 {
				t.Fatalf("%d versions after A and B from one head, want 2: B forked the chain", len(list.Versions))
			}
		})

		t.Run("ASweepWriteMovesTheBaseAndConflictsAndAHumanWriteClearsThem", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-a", domain.SourceSweep, cblock("a", "derived v1"))
			theirs := []domain.ChronicleBlock{cblock("a", "derived v2")}
			v2, err := s.AppendChronicleVersion(ctx, p, u.Page.ID, store.AppendChronicleVersionInput{
				ExpectedParentVersionID: u.Page.HeadVersionID, Blocks: []domain.ChronicleBlock{cblock("a", "mine")}, Source: domain.SourceHuman,
				Author: "sweeper", HasDerived: true, DerivedBlocks: theirs,
				Conflicts: []domain.ChronicleConflictItem{{BlockID: "a", Kind: domain.MergeConflict}},
			})
			noErr(t, err)
			page, err := s.GetChroniclePage(ctx, p, u.Page.ID)
			noErr(t, err)
			if page.Conflict == nil || page.Conflict.VersionID != v2.ID || len(page.Conflict.Items) != 1 || page.Conflict.TheirsObject != *page.LastDerivedObject {
				t.Fatalf("the conflict state = %+v (base %v)", page.Conflict, page.LastDerivedObject)
			}
			got, ok, err := s.GetChronicleConflictTheirs(ctx, p, u.Page.ID)
			noErr(t, err)
			if !ok || len(got) != 1 || got[0].Text != "derived v2" {
				t.Fatalf("theirs = %+v %v", got, ok)
			}
			// The first base was v1's own body, which v1 still names: it stays. Three bodies: v1, v2, base 2.
			if objs := pageObjects(t, s, p, u.Page.ID); len(objs) != 3 {
				t.Fatalf("bodies = %v, want 3", objs)
			}
			_, err = appendV(s, p, u.Page.ID, v2.ID, cblock("a", "landed"))
			noErr(t, err)
			page, err = s.GetChroniclePage(ctx, p, u.Page.ID)
			noErr(t, err)
			if page.Conflict != nil {
				t.Fatalf("a human write left the conflict standing: %+v", page.Conflict)
			}
			base, ok, err := s.GetChronicleBase(ctx, p, u.Page.ID)
			noErr(t, err)
			if !ok || len(base) != 1 || base[0].Text != "derived v2" {
				t.Fatalf("a human write moved the base: %+v", base)
			}
		})

		t.Run("RecordConflictsIsAgainstTheHeadAndReplacesItsBody", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-a", domain.SourceSweep, cblock("a", "v1"))
			rec := func(head, text string) error {
				return s.RecordChronicleConflicts(ctx, p, u.Page.ID, store.RecordChronicleConflictsInput{
					VersionID: head, Items: []domain.ChronicleConflictItem{{BlockID: "a", Kind: domain.MergeConflict}},
					Theirs: []domain.ChronicleBlock{cblock("a", text)}, Actor: "sweeper",
				})
			}
			noErr(t, rec(u.Page.HeadVersionID, "theirs 1"))
			noErr(t, rec(u.Page.HeadVersionID, "theirs 2"))
			got, ok, err := s.GetChronicleConflictTheirs(ctx, p, u.Page.ID)
			noErr(t, err)
			if !ok || got[0].Text != "theirs 2" {
				t.Fatalf("theirs = %+v %v, want the second record", got, ok)
			}
			// v1's body (also the base) and the second conflict body: the first is gone, not orphaned.
			if objs := pageObjects(t, s, p, u.Page.ID); len(objs) != 2 {
				t.Fatalf("bodies = %v, want 2: a re-record left its predecessor behind", objs)
			}
			wantErr(t, rec("CV-notthehead", "x"), domain.CodeStale, "")
			wantErr(t, s.RecordChronicleConflicts(ctx, p, "CP-missing", store.RecordChronicleConflictsInput{VersionID: "CV-x", Actor: "a"}), domain.CodeNotFound, "ChroniclePage")
		})

		t.Run("DeleteLeavesNoVersionAndNoBody", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			u := upsert(t, s, p, "adr/0001-a", domain.SourceSweep, cblock("a", "v1"))
			v2, err := appendV(s, p, u.Page.ID, u.Page.HeadVersionID, cblock("a", "v2"))
			noErr(t, err)
			keep := upsert(t, s, p, "adr/0002-keep", domain.SourceHuman, cblock("k", "kept"))
			// A stale pin refuses and deletes nothing.
			e := wantErr(t, s.DeleteChroniclePage(ctx, p, u.Page.ID, u.Page.HeadVersionID, "alice", nil), domain.CodeStale, "")
			if e.Details["actualVersionId"] != v2.ID {
				t.Fatalf("STALE = %+v", e.Details)
			}
			if objs := pageObjects(t, s, p, u.Page.ID); len(objs) == 0 {
				t.Fatal("a refused delete removed the bodies")
			}
			noErr(t, s.DeleteChroniclePage(ctx, p, u.Page.ID, v2.ID, "alice", nil))
			_, err = s.GetChroniclePage(ctx, p, u.Page.ID)
			wantErr(t, err, domain.CodeNotFound, "ChroniclePage")
			_, err = s.GetChronicleVersion(ctx, p, u.Page.ID, v2.ID)
			wantErr(t, err, domain.CodeNotFound, "ChronicleVersion")
			_, err = s.ListChronicleVersions(ctx, p, u.Page.ID, "", 0)
			wantErr(t, err, domain.CodeNotFound, "ChroniclePage")
			if objs := pageObjects(t, s, p, u.Page.ID); len(objs) != 0 {
				t.Fatalf("the delete left bodies %v", objs)
			}
			if objs := pageObjects(t, s, p, keep.Page.ID); len(objs) == 0 {
				t.Fatal("the delete took another page's bodies")
			}
			wantErr(t, s.DeleteChroniclePage(ctx, p, u.Page.ID, "", "alice", nil), domain.CodeNotFound, "ChroniclePage")
			// The slug is free again: a create starts a new chain.
			again := upsert(t, s, p, "adr/0001-a", domain.SourceHuman, cblock("a", "anew"))
			if !again.Created {
				t.Fatal("a deleted slug did not create again")
			}
		})

		t.Run("TwoProjectsShareAPageIdAndNothingElse", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			q, _, err := sd.seedProject(ctx)
			noErr(t, err)
			a := upsert(t, s, p, "adr/0001-same", domain.SourceHuman, cblock("a", "in p"))
			b := upsert(t, s, q, "adr/0001-same", domain.SourceHuman, cblock("a", "in q"))
			if a.Page.ID != b.Page.ID || !b.Created {
				t.Fatalf("the same slug in two projects: %s %s created=%v", a.Page.ID, b.Page.ID, b.Created)
			}
			_, err = s.GetChronicleVersion(ctx, q, a.Page.ID, a.Page.HeadVersionID)
			wantErr(t, err, domain.CodeNotFound, "ChronicleVersion")
			noErr(t, s.DeleteChroniclePage(ctx, p, a.Page.ID, "", "alice", nil))
			v, err := s.GetChronicleVersion(ctx, q, b.Page.ID, b.Page.HeadVersionID)
			noErr(t, err)
			if v.Blocks[0].Text != "in q" || len(pageObjects(t, s, q, b.Page.ID)) == 0 {
				t.Fatalf("deleting p's page touched q's: %+v", v)
			}
		})

		t.Run("ListPagesIsBySlugFilteredAndPaged", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			for _, slug := range []string{"c", "a", "b"} {
				upsert(t, s, p, slug, domain.SourceHuman)
			}
			_, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{ProjectID: p, Kind: domain.ChronicleTopic, Slug: "d", Title: "d", Source: domain.SourceHuman, Author: "a"})
			noErr(t, err)
			slugs := func(l store.ChroniclePageList) []string {
				out := []string{}
				for _, pg := range l.Pages {
					out = append(out, pg.Slug)
				}
				return out
			}
			all, err := s.ListChroniclePages(ctx, p, "", "", 0)
			noErr(t, err)
			if !slices.Equal(slugs(all), []string{"a", "b", "c", "d"}) || all.NextCursor != "" {
				t.Fatalf("pages = %v", slugs(all))
			}
			adrs, err := s.ListChroniclePages(ctx, p, domain.ChronicleADR, "", 2)
			noErr(t, err)
			rest, err := s.ListChroniclePages(ctx, p, domain.ChronicleADR, adrs.NextCursor, 2)
			noErr(t, err)
			if !slices.Equal(slugs(adrs), []string{"a", "b"}) || !slices.Equal(slugs(rest), []string{"c"}) || rest.NextCursor != "" {
				t.Fatalf("adr pages = %v then %v", slugs(adrs), slugs(rest))
			}
			_, err = s.ListChroniclePages(ctx, p, "", "not-a-cursor", 0)
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.ListChroniclePages(ctx, p, "nope", "", 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			bad := []store.UpsertChroniclePageInput{
				{ProjectID: p, Kind: "essay", Slug: "x", Title: "x", Source: domain.SourceHuman, Author: "a"},
				{ProjectID: p, Kind: domain.ChronicleADR, Slug: "", Title: "x", Source: domain.SourceHuman, Author: "a"},
				{ProjectID: p, Kind: domain.ChronicleADR, Slug: "x", Title: "x", Source: "robot", Author: "a"},
				{ProjectID: p, Kind: domain.ChronicleADR, Slug: "x", Title: "x", Source: domain.SourceHuman, Author: ""},
			}
			for _, in := range bad {
				_, err := s.UpsertChroniclePage(ctx, in)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.UpsertChroniclePage(ctx, store.UpsertChroniclePageInput{ProjectID: "P-missing", Kind: domain.ChronicleADR, Slug: "x", Title: "x", Source: domain.SourceHuman, Author: "a"})
			wantErr(t, err, domain.CodeNotFound, "Project")
			_, err = appendV(s, p, "CP-missing", "CV-x", cblock("a", "x"))
			wantErr(t, err, domain.CodeNotFound, "ChroniclePage")
			_, err = s.GetChronicleVersion(ctx, p, "CP-missing", "CV-x")
			wantErr(t, err, domain.CodeNotFound, "ChronicleVersion")
			_, _, err = s.GetChronicleBase(ctx, p, "CP-missing")
			wantErr(t, err, domain.CodeNotFound, "ChroniclePage")
			_, _, err = s.GetChronicleConflictTheirs(ctx, p, "CP-missing")
			wantErr(t, err, domain.CodeNotFound, "ChroniclePage")
			u := upsert(t, s, p, "x", domain.SourceHuman)
			_, err = s.ListChronicleVersions(ctx, p, u.Page.ID, "not-a-cursor", 0)
			wantErr(t, err, domain.CodeValidation, "")
			_, err = appendV(s, p, u.Page.ID, "", cblock("a", "x"))
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
