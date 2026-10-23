package domain

import (
	"reflect"
	"strings"
	"testing"
)

func blk(id, text string) ChronicleBlock {
	return ChronicleBlock{ID: id, State: BlockAuthored, Text: text, Citations: []ChronicleCitation{}}
}

func TestChroniclePageIDIsTheSlugHash(t *testing.T) {
	a, b := ChroniclePageID("adr/0001-x"), ChroniclePageID("adr/0001-x")
	if a != b || len(a) != 15 || !strings.HasPrefix(a, "CP-") {
		t.Fatalf("ChroniclePageID = %q, %q; want one stable CP- + 12 hex id", a, b)
	}
	if ChroniclePageID("adr/0002-x") == a {
		t.Fatal("two slugs share a page id")
	}
}

func TestResolveBlockState(t *testing.T) {
	h := BlockHash("  the text \n")
	if h != BlockHash("the text") {
		t.Fatal("whitespace at the ends changed the hash")
	}
	if got := ResolveBlockState("x", nil); got != BlockAuthored {
		t.Errorf("no origin = %s, want authored", got)
	}
	if got := ResolveBlockState("the text", &BlockOrigin{AdrID: "ADR-0001", BlockHash: h}); got != BlockDerived {
		t.Errorf("matching origin = %s, want derived", got)
	}
	if got := ResolveBlockState("edited", &BlockOrigin{AdrID: "ADR-0001", BlockHash: h}); got != BlockDerivedThenEdited {
		t.Errorf("edited text = %s, want derived-then-edited", got)
	}
	// A supplied state is never trusted.
	in := []ChronicleBlock{{ID: "a", State: BlockDerived, Text: "x"}}
	if got := WithComputedProvenance(in); got[0].State != BlockAuthored || got[0].Citations == nil {
		t.Errorf("WithComputedProvenance = %+v, want authored with [] citations", got[0])
	}
}

func TestNextRecordFieldsAbsentIsNotEmpty(t *testing.T) {
	full := CoverageFull
	existing := &ChronicleRecordFields{Topics: []string{"auth"}, Coverage: &full, SupersededBy: []string{"ADR-0001"}}
	kept := NextRecordFields(existing, ChronicleRecordFieldsInput{})
	if !SameRecordFields(kept, *existing) {
		t.Errorf("absent fields changed the record: %+v", kept)
	}
	cleared := NextRecordFields(existing, ChronicleRecordFieldsInput{Topics: []string{}, CoverageSet: true, SupersededBy: []string{}})
	if len(cleared.Topics) != 0 || cleared.Coverage != nil || len(cleared.SupersededBy) != 0 {
		t.Errorf("empty fields did not clear: %+v", cleared)
	}
	fresh := NextRecordFields(nil, ChronicleRecordFieldsInput{})
	if fresh.Topics == nil || fresh.SupersededBy == nil || fresh.Coverage != nil {
		t.Errorf("a new page's fields = %+v, want [] [] nil", fresh)
	}
}

func TestSameVisibleBlocks(t *testing.T) {
	a := []ChronicleBlock{blk("a", "x ")}
	b := []ChronicleBlock{blk("a", "x")}
	b[0].State = BlockDerived
	if !SameVisibleBlocks(a, b) {
		t.Error("trimmed-equal text with another state reads as changed")
	}
	c := []ChronicleBlock{blk("a", "x")}
	c[0].Citations = []ChronicleCitation{{ProjectID: "P", BoardID: "B", TaskID: "T-1"}}
	if SameVisibleBlocks(a, c) {
		t.Error("a citation-only change reads as unchanged")
	}
	d := []ChronicleBlock{blk("a", "x")}
	d[0].Origin = &BlockOrigin{AdrID: "ADR-0001", BlockHash: BlockHash("x")}
	if SameVisibleBlocks(a, d) {
		t.Error("an origin-only change reads as unchanged")
	}
}

// The merge table, one row per disposition and both silent absences.
func TestMergeVersions(t *testing.T) {
	type row struct {
		name               string
		base, ours, theirs []ChronicleBlock
		want               []MergeDisposition
		wantIDs            []string
		conflicts          []string
		source             ChronicleSource
	}
	rows := []row{
		{name: "taken-theirs: nobody edited it", base: []ChronicleBlock{blk("a", "v1")}, ours: []ChronicleBlock{blk("a", "v1")}, theirs: []ChronicleBlock{blk("a", "v2")},
			want: []MergeDisposition{TakenTheirs}, wantIDs: []string{"a"}, conflicts: []string{}, source: SourceSweep},
		{name: "kept-ours: a human edit the derivation did not touch", base: []ChronicleBlock{blk("a", "v1")}, ours: []ChronicleBlock{blk("a", "mine")}, theirs: []ChronicleBlock{blk("a", "v1")},
			want: []MergeDisposition{KeptOurs}, wantIDs: []string{"a"}, conflicts: []string{}, source: SourceHuman},
		{name: "conflict: both changed it", base: []ChronicleBlock{blk("a", "v1")}, ours: []ChronicleBlock{blk("a", "mine")}, theirs: []ChronicleBlock{blk("a", "v2")},
			want: []MergeDisposition{MergeConflict}, wantIDs: []string{"a"}, conflicts: []string{"a"}, source: SourceHuman},
		{name: "added-theirs: new in the derivation", base: nil, ours: nil, theirs: []ChronicleBlock{blk("a", "v1")},
			want: []MergeDisposition{AddedTheirs}, wantIDs: []string{"a"}, conflicts: []string{}, source: SourceSweep},
		{name: "conflict-restored-theirs: deleted by a human, changed by the derivation", base: []ChronicleBlock{blk("a", "v1")}, ours: nil, theirs: []ChronicleBlock{blk("a", "v2")},
			want: []MergeDisposition{ConflictRestoredTheirs}, wantIDs: []string{"a"}, conflicts: []string{"a"}, source: SourceHuman},
		{name: "a human deletion of an unchanged block stands", base: []ChronicleBlock{blk("a", "v1")}, ours: nil, theirs: []ChronicleBlock{blk("a", "v1")},
			want: []MergeDisposition{}, wantIDs: []string{}, conflicts: []string{}, source: SourceHuman},
		{name: "kept-ours-authored: the derivation never knew it", base: nil, ours: []ChronicleBlock{blk("h", "mine")}, theirs: nil,
			want: []MergeDisposition{KeptOursAuthored}, wantIDs: []string{"h"}, conflicts: []string{}, source: SourceHuman},
		{name: "kept-ours-orphaned: dropped by the derivation after a human edit", base: []ChronicleBlock{blk("a", "v1")}, ours: []ChronicleBlock{blk("a", "mine")}, theirs: nil,
			want: []MergeDisposition{KeptOursOrphaned}, wantIDs: []string{"a"}, conflicts: []string{"a"}, source: SourceHuman},
		{name: "dropped by the derivation, unedited: gone", base: []ChronicleBlock{blk("a", "v1")}, ours: []ChronicleBlock{blk("a", "v1")}, theirs: nil,
			want: []MergeDisposition{}, wantIDs: []string{}, conflicts: []string{}, source: SourceSweep},
		{name: "order follows theirs, ours-only blocks appended", base: []ChronicleBlock{blk("a", "1"), blk("b", "2")}, ours: []ChronicleBlock{blk("h", "mine"), blk("a", "1"), blk("b", "2")}, theirs: []ChronicleBlock{blk("b", "2"), blk("a", "1")},
			want: []MergeDisposition{TakenTheirs, TakenTheirs, KeptOursAuthored}, wantIDs: []string{"b", "a", "h"}, conflicts: []string{}, source: SourceHuman},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := MergeVersions(r.base, r.ours, r.theirs)
			disp, ids := []MergeDisposition{}, []string{}
			for _, m := range got.Blocks {
				disp, ids = append(disp, m.Disposition), append(ids, m.Block.ID)
			}
			if !reflect.DeepEqual(disp, r.want) || !reflect.DeepEqual(ids, r.wantIDs) {
				t.Errorf("dispositions %v ids %v, want %v %v", disp, ids, r.want, r.wantIDs)
			}
			if !reflect.DeepEqual(got.Conflicts, r.conflicts) || got.Source != r.source {
				t.Errorf("conflicts %v source %s, want %v %s", got.Conflicts, got.Source, r.conflicts, r.source)
			}
			items := ConflictItems(got)
			if len(items) != len(got.Conflicts) {
				t.Errorf("ConflictItems %v disagree with Conflicts %v", items, got.Conflicts)
			}
		})
	}
	t.Run("a conflict keeps ours and carries theirs", func(t *testing.T) {
		got := MergeVersions([]ChronicleBlock{blk("a", "v1")}, []ChronicleBlock{blk("a", "mine")}, []ChronicleBlock{blk("a", "v2")})
		m := got.Blocks[0]
		if m.Block.Text != "mine" || m.Theirs == nil || m.Theirs.Text != "v2" {
			t.Errorf("conflict block = %+v, want ours kept and theirs v2 carried", m)
		}
		if b := MergedBlocks(got); len(b) != 1 || b[0].Text != "mine" {
			t.Errorf("MergedBlocks = %+v", b)
		}
	})
}
