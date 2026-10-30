package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// chronicleBlobSHA is the blob the records embed; no file is needed, only its signed URL.
const chronicleBlobSHA = "a116e65f9c020dc5c38664593e74e1a5eda18119e716763789af34739f8ffe44"

var chronicleBlobURLRE = regexp.MustCompile(`https?://[^\s()]*?/blobs/` + chronicleBlobSHA + `\?exp=([0-9]+)&sig=[0-9a-f]+`)

// wantFreshBlobURL fails unless text embeds chronicleBlobSHA's download URL signed by core at the
// request base, valid at least 14 minutes from now: what a read must hand the browser.
func wantFreshBlobURL(t *testing.T, core *service.Core, where, text string) {
	t.Helper()
	m := chronicleBlobURLRE.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%s: no download URL for the blob in %q", where, text)
	}
	exp, _ := strconv.ParseInt(m[1], 10, 64)
	if until := time.Until(time.Unix(exp, 0)); until < 14*time.Minute {
		t.Fatalf("%s: the URL expires in %v, want a fresh BlobURLTTL: %q", where, until, m[0])
	}
	if want := core.SignBlobURL(context.Background(), chronicleBlobSHA, blob.Download, time.Unix(exp, 0)); m[0] != want {
		t.Fatalf("%s: URL %q is not the server's signature, want %q", where, m[0], want)
	}
}

// TestChronicleBlobURLs (P-244), on each engine, over REST: a record that embeds an attachment's
// live download URL keeps showing it after the URL's BlobURLTTL. The write stores the URL in its
// stable form (path-only, exp=0); every read (the page, a version, a conflict's theirs) signs it
// afresh; a round trip of the read text stores the same bytes and drifts no derived block; a
// re-ingest's expired URL for a blob the page held is stored stable too, while an expired URL the
// page never held is stored and read as written; a forged signature and a foreign URL are left byte
// for byte, on write and on read. The binding rules on every engine, and rows stored before the
// fix, are TestChronicleBlobURLsStayBound in internal/service.
func TestChronicleBlobURLs(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		ctx := context.Background()
		h := newStackHarness(t, s, Options{})
		pid, _ := seedBoard(t, h, s.Core, "Records")
		pc := "/api/projects/" + pid + "/chronicle"
		ok := func(code int, body string) map[string]any {
			t.Helper()
			if code != http.StatusOK {
				t.Fatalf("status %d %s, want 200", code, body)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		live := s.Core.SignBlobURL(ctx, chronicleBlobSHA, blob.Download, time.Now().Add(10*time.Minute).Truncate(time.Second))
		expired := s.Core.SignBlobURL(ctx, chronicleBlobSHA, blob.Download, time.Now().Add(-time.Hour).Truncate(time.Second))
		stable := strings.TrimPrefix(s.Core.SignBlobURL(ctx, chronicleBlobSHA, blob.Download, time.Unix(0, 0)), s.Core.BlobBase(ctx))
		forged := "http://servicetest.invalid/blobs/" + strings.Repeat("b", 64) + "?exp=" + strconv.FormatInt(time.Now().Unix()-60, 10) + "&sig=" + strings.Repeat("0", 64)
		foreign := "https://example.com/shot.png"
		text := "The screenshot:\n\n![shot](" + live + ")\n\n![forged](" + forged + ") ![foreign](" + foreign + ")"
		blockJSON := func(id, text string) string {
			b, _ := json.Marshal(map[string]any{"id": id, "text": text, "citations": []any{}, "origin": nil})
			return string(b)
		}
		stored := func(pageID, versionID string) []domain.ChronicleBlock {
			t.Helper()
			v, err := s.Store.GetChronicleVersion(ctx, pid, pageID, versionID)
			if err != nil {
				t.Fatal(err)
			}
			return v.Blocks
		}
		headText := func(view map[string]any, i int) string {
			return view["head"].(map[string]any)["blocks"].([]any)[i].(map[string]any)["text"].(string)
		}

		// The write: the genuine URL in its stable form, the forged and foreign ones untouched.
		created := ok(h.do("POST", pc, `{"kind":"adr","slug":"adr/shot","title":"Shot","blocks":[`+blockJSON("a-1", text)+`]}`, nil))
		pageID, v1 := created["pageId"].(string), created["versionId"].(string)
		want := "The screenshot:\n\n![shot](" + stable + ")\n\n![forged](" + forged + ") ![foreign](" + foreign + ")"
		if got := stored(pageID, v1)[0].Text; got != want {
			t.Fatalf("stored text %q, want the stable form %q", got, want)
		}

		// The page read: signed afresh at the request base; the others untouched.
		view := ok(h.do("GET", pc+"/"+pageID, "", nil))
		read := headText(view, 0)
		wantFreshBlobURL(t, s.Core, "GET page", read)
		if !strings.Contains(read, "![forged]("+forged+")") || !strings.Contains(read, "![foreign]("+foreign+")") {
			t.Fatalf("GET page rewrote a URL the server did not sign: %q", read)
		}

		// The version read.
		vview := ok(h.do("GET", pc+"/"+pageID+"/versions/"+v1, "", nil))
		wantFreshBlobURL(t, s.Core, "GET version", vview["version"].(map[string]any)["blocks"].([]any)[0].(map[string]any)["text"].(string))

		// The editor's round trip: the read text sent back stores the same bytes.
		edited := ok(h.do("POST", pc+"/"+pageID+"/edit", `{"expectedParentVersionId":"`+v1+`","blocks":[`+blockJSON("a-1", read)+`]}`, nil))
		if got := stored(pageID, edited["versionId"].(string))[0].Text; got != want {
			t.Fatalf("a round trip stored %q, want the same bytes %q", got, want)
		}

		// A sweep's block: derived on ingest (its origin hashes the text it sent), still derived after
		// the editor sends it back, and its conflict's theirs signed afresh.
		in := service.IngestChroniclePageReq{ProjectID: pid, Kind: "adr", Slug: "adr/swept", Title: "Swept",
			Blocks: []service.ChronicleBlockInput{{ID: "d-1", Text: text, Origin: &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(text)}}}}
		ing, err := s.Services.Chronicle.IngestPage(ctx, h.caller(), in)
		if err != nil {
			t.Fatal(err)
		}
		if b := stored(ing.PageID, ing.VersionID)[0]; b.State != domain.BlockDerived || b.Text != want {
			t.Fatalf("ingested block %s %q, want derived in the stable form", b.State, b.Text)
		}
		sp := pc + "/" + ing.PageID
		sread := headText(ok(h.do("GET", sp, "", nil)), 0)
		back := ok(h.do("POST", sp+"/edit", `{"expectedParentVersionId":"`+ing.VersionID+`","blocks":[`+blockJSON("d-1", sread)+`]}`, nil))
		if d := back["drifted"].([]any); len(d) != 0 {
			t.Fatalf("sending a derived block back as read drifted it: %v", back)
		}
		human := ok(h.do("POST", sp+"/edit", `{"expectedParentVersionId":"`+back["versionId"].(string)+`","blocks":[`+blockJSON("d-1", "A human rewrite.")+`]}`, nil))
		swept := "The sweep's rewrite:\n\n![shot](" + expired + ")"
		in.Blocks = []service.ChronicleBlockInput{{ID: "d-1", Text: swept, Origin: &domain.BlockOrigin{AdrID: "ADR-0001", BlockHash: domain.BlockHash(swept)}}}
		re, err := s.Services.Chronicle.IngestPage(ctx, h.caller(), in)
		if err != nil || len(re.Conflicts) != 1 {
			t.Fatalf("re-ingest over %v: %+v, %v, want one conflict", human, re, err)
		}
		cview := ok(h.do("GET", sp, "", nil))
		items := cview["conflicts"].(map[string]any)["items"].([]any)
		wantFreshBlobURL(t, s.Core, "GET page conflict theirs", items[0].(map[string]any)["theirs"].(map[string]any)["text"].(string))

		// An expired URL on a page that never held the blob: stored and read as written, dead.
		lone := "Pasted late: ![shot](" + expired + ")"
		late := ok(h.do("POST", pc, `{"kind":"adr","slug":"adr/late","title":"Late","blocks":[`+blockJSON("a-1", lone)+`]}`, nil))
		if got := stored(late["pageId"].(string), late["versionId"].(string))[0].Text; got != lone {
			t.Fatalf("an unheld expired URL stored %q, want it as written %q", got, lone)
		}
		if got := headText(ok(h.do("GET", pc+"/"+late["pageId"].(string), "", nil)), 0); got != lone {
			t.Fatalf("an unheld expired URL read %q, want it as written %q", got, lone)
		}
		t.Logf("%s: stored %q; read %q", s.Engine, want, read)
	})
}
