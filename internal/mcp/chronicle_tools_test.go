package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The Chronicle tools on the real stack, every selected engine (BATONDECK_TEST_ENGINE): each tool's
// happy path and each error it declares (NOT_FOUND, STALE on the page's head, VALIDATION, the cap
// included, and FORBIDDEN across projects).

// chronicleObjects is the one store read these tests make beside the tools: the body keys a delete
// must leave none of.
type chronicleObjects interface {
	ListChronicleObjects(ctx context.Context, projectID string) ([]string, error)
}

type chronicleRig struct {
	t      *testing.T
	ada    *caller
	grace  *caller
	store  chronicleObjects
	p, q   string // ada's project, grace's project
	engine string
}

// chronicleStacks runs fn once per selected engine over a real core whose blob.max_bytes is
// maxBytes (0: the default), with ada in project p and grace, a member of nothing of ada's, in q.
func chronicleStacks(t *testing.T, maxBytes int64, fn func(t *testing.T, r *chronicleRig)) {
	for _, e := range servicetest.Engines(t) {
		t.Run(e, func(t *testing.T) {
			st := servicetest.OpenStore(t, e)
			c := service.New(st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1, Media: service.MediaOptions{MaxBytes: maxBytes}})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			svc := servicetest.ServicesOf(c)
			h := bk.BootHarness(t, svc, stackResolver())
			tools.Register(h.Server(), svc)
			r := &chronicleRig{t: t, store: st, engine: e,
				ada:   &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}},
				grace: &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}},
			}
			r.p = str(r.ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
			r.q = str(r.grace.call("create_project", map[string]any{"name": "Grace's"})["projectId"])
			fn(t, r)
		})
	}
}

func cblocks(pairs ...string) []any {
	out := []any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]any{"id": pairs[i], "text": pairs[i+1]})
	}
	return out
}

// wantCode is a call that fails with code, its error checked against contract/errors.json.
func (r *chronicleRig) wantCode(c *caller, tool string, args map[string]any, code string) map[string]any {
	r.t.Helper()
	e := bk.ToolError(r.t, c.cs, tool, args)
	if e["code"] != code {
		r.t.Fatalf("%s %v: %v, want %s", tool, args, e, code)
	}
	c.called[tool+":"+code]++
	return e
}

func (r *chronicleRig) read(c *caller, uri string, into any) {
	r.t.Helper()
	res, err := c.cs.ReadResource(testCtx(r.t), &sdk.ReadResourceParams{URI: uri})
	if err != nil || len(res.Contents) != 1 {
		r.t.Fatalf("read %s: %+v, %v", uri, res, err)
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), into); err != nil {
		r.t.Fatalf("read %s: %v in %s", uri, err, res.Contents[0].Text)
	}
}

func (r *chronicleRig) uri(project, path string) string {
	return bk.Scheme + "://" + project + "/chronicle" + path
}

type historyRead struct {
	Versions []struct {
		ID              string  `json:"id"`
		ParentVersionID *string `json:"parentVersionId"`
	} `json:"versions"`
}

func TestChronicleCreatePage(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		args := map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-create", "title": "Create", "blocks": cblocks("a-1", "by hand")}
		made := r.ada.call("create_chronicle_page", args)
		if str(made["pageId"]) != domain.ChroniclePageID("adr/0001-create") || !strings.HasPrefix(str(made["versionId"]), "CV-") {
			t.Fatalf("create_chronicle_page = %v", made)
		}
		var view struct {
			Page struct{ Title string } `json:"page"`
			Head struct {
				Blocks []domain.ChronicleBlock `json:"blocks"`
			} `json:"head"`
		}
		r.read(r.ada, r.uri(r.p, "/"+str(made["pageId"])), &view)
		if view.Page.Title != "Create" || len(view.Head.Blocks) != 1 || view.Head.Blocks[0].State != domain.BlockAuthored {
			t.Fatalf("the created page reads %+v", view)
		}
		t.Run("VALIDATION: a taken slug, refused inside the write", func(t *testing.T) {
			again := map[string]any{"projectId": r.p, "kind": "topic", "slug": "adr/0001-create", "title": "Other", "blocks": cblocks()}
			e := r.wantCode(r.ada, "create_chronicle_page", again, "VALIDATION")
			if d := obj(e["details"]); d["pageId"] != str(made["pageId"]) {
				t.Fatalf("details %v", d)
			}
			r.read(r.ada, r.uri(r.p, "/"+str(made["pageId"])), &view)
			if view.Page.Title != "Create" {
				t.Fatalf("the refused create renamed the page to %q", view.Page.Title)
			}
		})
		t.Run("VALIDATION: a sweep-shaped block id, a bad slug, a duplicate id", func(t *testing.T) {
			for _, bad := range []map[string]any{
				{"projectId": r.p, "kind": "adr", "slug": "adr/0002-x", "title": "x", "blocks": cblocks("dec1/1", "x")},
				{"projectId": r.p, "kind": "adr", "slug": "ADR/../x", "title": "x", "blocks": cblocks()},
				{"projectId": r.p, "kind": "adr", "slug": "adr/0003-x", "title": "x", "blocks": cblocks("a", "1", "a", "2")},
				{"projectId": r.p, "kind": "essay", "slug": "adr/0004-x", "title": "x", "blocks": cblocks()},
			} {
				r.wantCode(r.ada, "create_chronicle_page", bad, "VALIDATION")
			}
		})
		t.Run("FORBIDDEN: another project's", func(t *testing.T) {
			r.wantCode(r.grace, "create_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0009-g", "title": "g", "blocks": cblocks()}, "FORBIDDEN")
		})
	})
}

func TestChronicleEditPage(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		made := r.ada.call("create_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-edit", "title": "Edit", "blocks": cblocks("a-1", "v1")})
		page, v1 := str(made["pageId"]), str(made["versionId"])
		edit := func(parent, text string) map[string]any {
			return map[string]any{"projectId": r.p, "pageId": page, "expectedParentVersionId": parent, "blocks": cblocks("a-1", text)}
		}
		v2 := str(r.ada.call("edit_chronicle_page", edit(v1, "v2"))["versionId"])
		third := r.ada.call("edit_chronicle_page", edit(v2, "v3"))
		v3 := str(third["versionId"])
		// AC2: two edits are three versions, the head on the third, each the child of the one before.
		var hist historyRead
		r.read(r.ada, r.uri(r.p, "/"+page+"/versions"), &hist)
		if len(hist.Versions) != 3 || hist.Versions[0].ID != v3 || *hist.Versions[0].ParentVersionID != v2 || *hist.Versions[1].ParentVersionID != v1 || hist.Versions[2].ParentVersionID != nil {
			t.Fatalf("history after two edits = %+v, want v3 <- v2 <- v1", hist)
		}
		var view struct {
			Page struct {
				HeadVersionID string `json:"headVersionId"`
			} `json:"page"`
		}
		r.read(r.ada, r.uri(r.p, "/"+page), &view)
		if view.Page.HeadVersionID != v3 {
			t.Fatalf("head %s, want the third version %s", view.Page.HeadVersionID, v3)
		}
		t.Logf("%s: 2 edits -> %d versions, head %s (version 3 of 3)", r.engine, len(hist.Versions), v3)
		t.Run("STALE: an edit from a version that is not the head", func(t *testing.T) {
			e := r.wantCode(r.ada, "edit_chronicle_page", edit(v2, "lost"), "STALE")
			if d := obj(e["details"]); d["expectedVersionId"] != v2 || d["actualVersionId"] != v3 || e["retryable"] != true {
				t.Fatalf("STALE = %v", e)
			}
			r.read(r.ada, r.uri(r.p, "/"+page+"/versions"), &hist)
			if len(hist.Versions) != 3 {
				t.Fatalf("the STALE edit wrote a version: %d", len(hist.Versions))
			}
		})
		t.Run("NOT_FOUND: an unknown page", func(t *testing.T) {
			r.wantCode(r.ada, "edit_chronicle_page", map[string]any{"projectId": r.p, "pageId": "CP-000000000000", "expectedParentVersionId": v3, "blocks": cblocks()}, "NOT_FOUND")
		})
		t.Run("VALIDATION: a duplicate block id", func(t *testing.T) {
			r.wantCode(r.ada, "edit_chronicle_page", map[string]any{"projectId": r.p, "pageId": page, "expectedParentVersionId": v3, "blocks": cblocks("a", "1", "a", "2")}, "VALIDATION")
		})
		t.Run("FORBIDDEN across projects, and another project's page id is NOT_FOUND in one's own", func(t *testing.T) {
			r.wantCode(r.grace, "edit_chronicle_page", edit(v3, "theirs"), "FORBIDDEN")
			r.wantCode(r.grace, "edit_chronicle_page", map[string]any{"projectId": r.q, "pageId": page, "expectedParentVersionId": v3, "blocks": cblocks()}, "NOT_FOUND")
		})
		t.Run("origin is inherited, never taken: an edited derived block drifts", func(t *testing.T) {
			hash := domain.BlockHash("derived")
			ing := r.ada.call("ingest_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0002-swept", "title": "Swept",
				"blocks": []any{map[string]any{"id": "dec1/1", "text": "derived", "origin": map[string]any{"adrId": "ADR-0002", "blockHash": hash}}}})
			forged := map[string]any{"projectId": r.p, "pageId": str(ing["pageId"]), "expectedParentVersionId": str(ing["versionId"]),
				"blocks": []any{map[string]any{"id": "dec1/1", "text": "rewritten", "origin": map[string]any{"adrId": "ADR-0002", "blockHash": domain.BlockHash("rewritten")}}}}
			got := r.ada.call("edit_chronicle_page", forged)
			if d, _ := got["drifted"].([]any); len(d) != 1 || d[0] != "dec1/1" {
				t.Fatalf("drifted = %v, want [dec1/1]: the caller's forged origin was trusted", got["drifted"])
			}
		})
	})
}

func TestChronicleIngestPage(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		ingest := func(blocks []any) map[string]any {
			return map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-ingest", "title": "Ingest", "topics": []any{"auth"}, "blocks": blocks}
		}
		first := r.ada.call("ingest_chronicle_page", ingest(cblocks("dec1/1", "one", "dec1/2", "two")))
		if first["created"] != true || first["unchanged"] != false {
			t.Fatalf("first ingest = %v", first)
		}
		again := r.ada.call("ingest_chronicle_page", ingest(cblocks("dec1/1", "one", "dec1/2", "two")))
		if again["created"] != false || again["unchanged"] != true || again["versionId"] != first["versionId"] {
			t.Fatalf("an unchanged re-ingest = %v, want no new version", again)
		}
		page := str(first["pageId"])
		// A human edits block 1; the derivation then changes both: 1 conflicts (ours kept), 2 is taken.
		r.ada.call("edit_chronicle_page", map[string]any{"projectId": r.p, "pageId": page, "expectedParentVersionId": str(first["versionId"]), "blocks": cblocks("dec1/1", "mine", "dec1/2", "two")})
		merged := r.ada.call("ingest_chronicle_page", ingest(cblocks("dec1/1", "one v2", "dec1/2", "two v2")))
		if c, _ := merged["conflicts"].([]any); merged["unchanged"] != false || len(c) != 1 || c[0] != "dec1/1" {
			t.Fatalf("the merge = %v, want a version with conflict dec1/1", merged)
		}
		var view struct {
			Head struct {
				Blocks []domain.ChronicleBlock `json:"blocks"`
			} `json:"head"`
			Conflicts struct {
				Items []struct {
					BlockID string                 `json:"blockId"`
					Theirs  *domain.ChronicleBlock `json:"theirs"`
				} `json:"items"`
			} `json:"conflicts"`
		}
		r.read(r.ada, r.uri(r.p, "/"+page), &view)
		if len(view.Head.Blocks) != 2 || view.Head.Blocks[0].Text != "mine" || view.Head.Blocks[1].Text != "two v2" {
			t.Fatalf("merged head = %+v, want ours kept and theirs taken", view.Head.Blocks)
		}
		if len(view.Conflicts.Items) != 1 || view.Conflicts.Items[0].Theirs == nil || view.Conflicts.Items[0].Theirs.Text != "one v2" {
			t.Fatalf("conflicts = %+v, want dec1/1 with theirs", view.Conflicts)
		}
		t.Run("FORBIDDEN: another project's", func(t *testing.T) {
			args := ingest(cblocks())
			r.wantCode(r.grace, "ingest_chronicle_page", args, "FORBIDDEN")
		})
		t.Run("VALIDATION: a bad topic slug", func(t *testing.T) {
			args := ingest(cblocks())
			args["topics"] = []any{"Not A Slug"}
			r.wantCode(r.ada, "ingest_chronicle_page", args, "VALIDATION")
		})
	})
}

// TestChronicleIngestCap is AC3: a document one byte over blob.max_bytes is VALIDATION and writes
// nothing; one exactly at it is a page whose stored body reads back byte for byte.
func TestChronicleIngestCap(t *testing.T) {
	text := "the record, with a non-ASCII é and a quote \" inside"
	body := domain.ChronicleBody([]domain.ChronicleBlock{{ID: "dec1/1", Text: text}})
	chronicleStacks(t, int64(len(body)), func(t *testing.T, r *chronicleRig) {
		over := map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-over", "title": "Over", "blocks": cblocks("dec1/1", text+"x")}
		e := r.wantCode(r.ada, "ingest_chronicle_page", over, "VALIDATION")
		if d := obj(e["details"]); d["bytes"] != float64(len(body)+1) || d["max"] != float64(len(body)) {
			t.Fatalf("the cap refusal names %v, want bytes %d max %d", d, len(body)+1, len(body))
		}
		var missing struct {
			Page any `json:"page"`
		}
		r.read(r.ada, r.uri(r.p, "/"+domain.ChroniclePageID("adr/0001-over")), &missing)
		if missing.Page != nil {
			t.Fatal("the refused ingest wrote a page")
		}
		at := r.ada.call("ingest_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0002-at", "title": "At", "blocks": cblocks("dec1/1", text)})
		res, err := r.ada.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: r.uri(r.p, "/"+str(at["pageId"])+"/versions/"+str(at["versionId"]))})
		if err != nil {
			t.Fatal(err)
		}
		var read struct {
			Version struct {
				Blocks json.RawMessage `json:"blocks"`
			} `json:"version"`
		}
		if err := json.Unmarshal([]byte(res.Contents[0].Text), &read); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(read.Version.Blocks, body) {
			t.Fatalf("the stored body reads back as\n%s\nwant\n%s", read.Version.Blocks, body)
		}
		t.Logf("%s: cap %d bytes; %d bytes refused VALIDATION, %d bytes stored and read back byte-identical", r.engine, len(body), len(body)+1, len(read.Version.Blocks))
		// The cap is every write's: an edit one byte over is refused too.
		r.wantCode(r.ada, "edit_chronicle_page", map[string]any{"projectId": r.p, "pageId": str(at["pageId"]), "expectedParentVersionId": str(at["versionId"]), "blocks": cblocks("dec1/1", text+"y")}, "VALIDATION")
	})
}

func TestChronicleDeletePage(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		made := r.ada.call("ingest_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-del", "title": "Del", "blocks": cblocks("dec1/1", "v1")})
		page, v1 := str(made["pageId"]), str(made["versionId"])
		v2 := str(r.ada.call("edit_chronicle_page", map[string]any{"projectId": r.p, "pageId": page, "expectedParentVersionId": v1, "blocks": cblocks("dec1/1", "v2")})["versionId"])
		kept := str(r.ada.call("create_chronicle_page", map[string]any{"projectId": r.p, "kind": "topic", "slug": "topic/kept", "title": "Kept", "blocks": cblocks("a", "kept")})["pageId"])
		bodies := func(pageID string) int {
			keys, err := r.store.ListChronicleObjects(context.Background(), r.p)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, k := range keys {
				if strings.HasPrefix(k, pageID+"/") {
					n++
				}
			}
			return n
		}
		before := bodies(page)
		t.Run("STALE: a pin on a head that moved deletes nothing", func(t *testing.T) {
			e := r.wantCode(r.ada, "delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page, "expectedHeadVersionId": v1}, "STALE")
			if d := obj(e["details"]); d["actualVersionId"] != v2 {
				t.Fatalf("STALE = %v", e)
			}
			if bodies(page) != before {
				t.Fatal("the refused delete removed bodies")
			}
		})
		t.Run("FORBIDDEN across projects, NOT_FOUND by another project's id", func(t *testing.T) {
			r.wantCode(r.grace, "delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page}, "FORBIDDEN")
			r.wantCode(r.grace, "delete_chronicle_page", map[string]any{"projectId": r.q, "pageId": page}, "NOT_FOUND")
		})
		if got := r.ada.call("delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page, "expectedHeadVersionId": v2}); got["ok"] != true {
			t.Fatalf("delete_chronicle_page = %v", got)
		}
		// AC4: no version and no body of the page is left; another page keeps its own.
		var hist historyRead
		r.read(r.ada, r.uri(r.p, "/"+page+"/versions"), &hist)
		var ver struct {
			Version any `json:"version"`
		}
		r.read(r.ada, r.uri(r.p, "/"+page+"/versions/"+v1), &ver)
		if len(hist.Versions) != 0 || ver.Version != nil || bodies(page) != 0 || bodies(kept) == 0 {
			t.Fatalf("after the delete: %d versions, version %v, %d bodies of the page, %d of the kept page", len(hist.Versions), ver.Version, bodies(page), bodies(kept))
		}
		t.Logf("%s: %d bodies before the delete, 0 after; 0 versions; the other page keeps %d", r.engine, before, bodies(kept))
		// No version is left to say the page existed, so the audit log does: who, which page, which
		// project. The STALE refusal above wrote none.
		audit, ok := r.store.(interface {
			ListAuditLog(ctx context.Context, f store.AuditFilter, cursor string, limit int) (store.AuditPage, error)
		})
		if !ok {
			t.Fatalf("%T reads no audit log", r.store)
		}
		rows, err := audit.ListAuditLog(context.Background(), store.AuditFilter{Action: "chronicle.page_deleted"}, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows.Rows) != 1 || rows.Rows[0].Actor != "u-ada" || rows.Rows[0].Subject != page || rows.Rows[0].ProjectID != r.p || rows.Rows[0].Result != store.AuditOK {
			t.Fatalf("audit rows for the delete: %+v, want one chronicle.page_deleted by u-ada on %s in %s", rows.Rows, page, r.p)
		}
		t.Run("NOT_FOUND: a page already deleted", func(t *testing.T) {
			r.wantCode(r.ada, "delete_chronicle_page", map[string]any{"projectId": r.p, "pageId": page}, "NOT_FOUND")
		})
	})
}

// TestChronicleResourcesReadAsConductorDoes: a missing page is {page: null, head: null}, a history
// pages by nextCursor, and a member of another project cannot read them.
func TestChronicleResourcesReadAsConductorDoes(t *testing.T) {
	chronicleStacks(t, 0, func(t *testing.T, r *chronicleRig) {
		res, err := r.ada.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: r.uri(r.p, "/CP-000000000000")})
		if err != nil || res.Contents[0].Text != `{"page":null,"head":null}` {
			t.Fatalf("a missing page reads %+v, %v", res, err)
		}
		made := r.ada.call("create_chronicle_page", map[string]any{"projectId": r.p, "kind": "adr", "slug": "adr/0001-r", "title": "R", "blocks": cblocks()})
		var index struct {
			Pages      []struct{ ID string } `json:"pages"`
			NextCursor *string               `json:"nextCursor"`
		}
		r.read(r.ada, r.uri(r.p, ""), &index)
		if len(index.Pages) != 1 || index.Pages[0].ID != str(made["pageId"]) || index.NextCursor != nil {
			t.Fatalf("index = %+v", index)
		}
		// Each read is refused to a member of another project; ada's same reads are the positive control.
		page, version := str(made["pageId"]), str(made["versionId"])
		for _, path := range []string{"", "/" + page, "/" + page + "/versions", "/" + page + "/versions/" + version} {
			if _, err := r.ada.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: r.uri(r.p, path)}); err != nil {
				t.Fatalf("ada reads %s: %v", r.uri(r.p, path), err)
			}
			if _, err := r.grace.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: r.uri(r.p, path)}); err == nil || !strings.Contains(err.Error(), "Not a member of project "+r.p) {
				t.Fatalf("a member of another project reads %s: %v, want the membership refusal", r.uri(r.p, path), err)
			}
		}
	})
}
