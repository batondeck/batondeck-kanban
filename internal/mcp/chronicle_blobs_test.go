package mcp_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// TestChronicleBlobURLs (P-244): over MCP on each engine, a record created with an attachment's
// download URL past its expiry reads back, on the chronicle-page and chronicle-version resources,
// with that blob's URL signed afresh by the server, valid BlobURLTTL from the read.
func TestChronicleBlobURLs(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		ctx := context.Background()
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		p := str(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		sha := "a116e65f9c020dc5c38664593e74e1a5eda18119e716763789af34739f8ffe44"
		expired := s.Core.SignBlobURL(ctx, sha, blob.Download, time.Now().Add(-time.Hour).Truncate(time.Second))
		created := ada.call("create_chronicle_page", map[string]any{"projectId": p, "kind": "adr", "slug": "adr/shot", "title": "Shot",
			"blocks": cblocks("a-1", "![shot]("+expired+")")})
		page, version := str(created["pageId"]), str(created["versionId"])
		re := regexp.MustCompile(`\(([^)]*/blobs/` + sha + `\?exp=([0-9]+)&sig=[0-9a-f]+)\)`)
		fresh := func(uri string, text func(map[string]any) string) {
			t.Helper()
			res, err := ada.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: uri})
			if err != nil || len(res.Contents) != 1 {
				t.Fatalf("read %s: %+v, %v", uri, res, err)
			}
			var v map[string]any
			if err := json.Unmarshal([]byte(res.Contents[0].Text), &v); err != nil {
				t.Fatal(err)
			}
			got := text(v)
			m := re.FindStringSubmatch(got)
			if m == nil {
				t.Fatalf("%s: no URL for the blob in %q", uri, got)
			}
			exp, _ := strconv.ParseInt(m[2], 10, 64)
			if want := s.Core.SignBlobURL(ctx, sha, blob.Download, time.Unix(exp, 0)); m[1] != want || time.Until(time.Unix(exp, 0)) < 14*time.Minute {
				t.Fatalf("%s: URL %q, want the server's, valid 15 minutes (%q)", uri, m[1], want)
			}
			t.Logf("%s %s: %s", s.Engine, uri, got)
		}
		block := func(v map[string]any) string {
			return str(v["blocks"].([]any)[0].(map[string]any)["text"])
		}
		base := bk.Scheme + "://" + p + "/chronicle/" + page
		fresh(base, func(v map[string]any) string { return block(v["head"].(map[string]any)) })
		fresh(base+"/versions/"+version, func(v map[string]any) string { return block(v["version"].(map[string]any)) })
	})
}
