package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// P-244: with no signer (serve found no keyring) a record's text is stored and read as written,
// a URL that reads as a signed blob URL included: nothing can tell a genuine one.
func TestChronicleBlobURLsWithoutSigner(t *testing.T) {
	f := newFixture(t, Options{})
	other, err := blob.NewSigner([]byte("another server's signing key, 32 bytes"))
	if err != nil {
		t.Fatal(err)
	}
	text := "![shot](" + other.URL("http://elsewhere.invalid", "a116e65f9c020dc5c38664593e74e1a5eda18119e716763789af34739f8ffe44", blob.Download, time.Unix(1, 0)) + ")"
	ch := f.c.Chronicle()
	created, err := ch.CreatePage(f.ctx, f.alice, CreateChroniclePageReq{ProjectID: f.project.ID, Kind: "adr", Slug: "adr/plain", Title: "Plain",
		Blocks: []ChronicleBlockInput{{ID: "a-1", Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.st.GetChronicleVersion(f.ctx, f.project.ID, created.PageID, created.VersionID)
	if err != nil || v.Blocks[0].Text != text {
		t.Fatalf("stored %+v, %v, want the text as written", v.Blocks, err)
	}
	view, err := ch.GetPage(f.ctx, f.alice, GetChroniclePageReq{ProjectID: f.project.ID, PageID: created.PageID})
	if err != nil || view.Head.Blocks[0].Text != text || view.Head.Blocks[0].State != domain.BlockAuthored {
		t.Fatalf("read %+v, %v, want the text as written", view.Head, err)
	}
}
