package service

import (
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// A both-changed conflict is settled by the human write that resolves it, whichever side it took:
// the next ingest of the same derivation flags nothing, and a block landed as the generator's text
// is derived again.
func TestChronicleResolvedConflictStaysSettled(t *testing.T) {
	origin := func(text string) *domain.BlockOrigin {
		return &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(text)}
	}
	for _, c := range []struct {
		name, landed string
		want         domain.BlockState
	}{
		{name: "take theirs", landed: "the generator's text", want: domain.BlockDerived},
		{name: "keep ours", landed: "the human's text", want: domain.BlockDerivedThenEdited},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, Options{})
			ch := f.c.Chronicle()
			ingest := func(text string) IngestChroniclePageResp {
				t.Helper()
				r, err := ch.IngestPage(f.ctx, f.alice, IngestChroniclePageReq{ProjectID: f.project.ID, Kind: "adr", Slug: "adr/0001-a", Title: "A",
					Blocks: []ChronicleBlockInput{{ID: "dec1/1", Text: text, Origin: origin(text)}}})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			edit := func(page, parent, text string) string {
				t.Helper()
				r, err := ch.EditPage(f.ctx, f.alice, EditChroniclePageReq{ProjectID: f.project.ID, PageID: page, ExpectedParentVersionID: parent,
					Blocks: []ChronicleBlockInput{{ID: "dec1/1", Text: text}}})
				if err != nil {
					t.Fatal(err)
				}
				return r.VersionID
			}
			first := ingest("the first derivation")
			head := edit(first.PageID, first.VersionID, "the human's text")
			if r := ingest("the generator's text"); len(r.Conflicts) != 1 || !r.Unchanged {
				t.Fatalf("the both-changed ingest = %+v, want one conflict and no version", r)
			}
			head = edit(first.PageID, head, c.landed)
			for i := range 2 {
				r := ingest("the generator's text")
				if len(r.Conflicts) != 0 {
					t.Fatalf("ingest %d after the resolution = %+v, want no conflict: a settled conflict came back", i+1, r)
				}
				view, err := ch.GetPage(f.ctx, f.alice, GetChroniclePageReq{ProjectID: f.project.ID, PageID: first.PageID})
				if err != nil {
					t.Fatal(err)
				}
				if view.Page.Conflict != nil {
					t.Fatalf("ingest %d after the resolution left the page flagged: %+v", i+1, view.Page.Conflict)
				}
				if b := view.Head.Blocks[0]; b.Text != c.landed || b.State != c.want {
					t.Fatalf("ingest %d after the resolution: block %q %s, want %q %s", i+1, b.Text, b.State, c.landed, c.want)
				}
			}
		})
	}
}
