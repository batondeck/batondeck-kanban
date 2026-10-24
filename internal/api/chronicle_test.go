package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The chronicle routes on each engine, in the bodies web/src/api/chronicle.ts sends: a member
// creates, lists, reads, edits, reads the history and one version, reverts and deletes, each answer
// in the keys the screens read. The refusals: a non-member is 403 FORBIDDEN on all eight (the
// member's 200 on the same paths is the control); a page reached through another of the member's
// projects is absent (null, NOT_FOUND), never the page; a stale parent or pinned head is 409 STALE;
// a DELETE's body field is 400.
func TestChronicleRoutesOnTheStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, _ := seedBoard(t, h, s.Core, "Records")
		other, _ := seedBoard(t, h, s.Core, "Elsewhere")
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
		refused := func(code int, body string, status int, want string) {
			t.Helper()
			var e struct{ Code string }
			_ = json.Unmarshal([]byte(body), &e)
			if code != status || e.Code != want {
				t.Fatalf("%d %s, want %d %s", code, body, status, want)
			}
		}

		created := ok(h.do("POST", pc, `{"kind":"adr","slug":"adr/r2-7","title":"Routes","idempotencyKey":"k-1",`+
			`"blocks":[{"id":"a-1","text":"The body."},{"id":"a-2","text":"`+"```mermaid\\ngraph TD; A-->B;\\n```"+`"}]}`, nil))
		pageID, v1 := created["pageId"].(string), created["versionId"].(string)
		if pageID == "" || v1 == "" {
			t.Fatalf("POST chronicle: %v", created)
		}
		replayed := ok(h.do("POST", pc, `{"kind":"adr","slug":"adr/r2-7","title":"Routes","idempotencyKey":"k-1",`+
			`"blocks":[{"id":"a-1","text":"The body."},{"id":"a-2","text":"`+"```mermaid\\ngraph TD; A-->B;\\n```"+`"}]}`, nil))
		if replayed["pageId"] != pageID {
			t.Fatalf("a replayed create: %v, want page %s", replayed, pageID)
		}
		page := pc + "/" + pageID

		index := ok(h.do("GET", pc, "", nil))
		if pages := index["pages"].([]any); len(pages) != 1 || pages[0].(map[string]any)["id"] != pageID {
			t.Fatalf("GET chronicle: %v", index)
		}
		if _, more := index["nextCursor"]; more {
			t.Fatalf("GET chronicle: nextCursor on a complete index: %v", index)
		}
		view := ok(h.do("GET", page, "", nil))
		for _, k := range []string{"page", "head", "drifted", "conflicts"} {
			if _, has := view[k]; !has {
				t.Fatalf("GET page has no %s: %v", k, view)
			}
		}
		head := view["head"].(map[string]any)
		if head["id"] != v1 || len(head["blocks"].([]any)) != 2 || view["page"].(map[string]any)["headVersionId"] != v1 {
			t.Fatalf("GET page: %v", view)
		}

		// The SPA's edit sends citations and origin: null on every block (Chronicle.tsx mergeDraft).
		edited := ok(h.do("POST", page+"/edit", `{"expectedParentVersionId":"`+v1+`","blocks":[{"id":"a-1","text":"The body, edited.","citations":[],"origin":null}]}`, nil))
		v2 := edited["versionId"].(string)
		if v2 == "" || v2 == v1 || len(edited["drifted"].([]any)) != 0 {
			t.Fatalf("POST edit: %v", edited)
		}
		code, body := h.do("POST", page+"/edit", `{"expectedParentVersionId":"`+v1+`","blocks":[]}`, nil)
		refused(code, body, http.StatusConflict, "STALE")

		history := ok(h.do("GET", page+"/versions", "", nil))
		vs := history["versions"].([]any)
		if len(vs) != 2 || vs[0].(map[string]any)["id"] != v2 || vs[1].(map[string]any)["id"] != v1 {
			t.Fatalf("GET versions: %v, want %s then %s", history, v2, v1)
		}
		first := ok(h.do("GET", page+"/versions/"+v1, "", nil))["version"].(map[string]any)
		if first["id"] != v1 || len(first["blocks"].([]any)) != 2 {
			t.Fatalf("GET version: %v", first)
		}

		reverted := ok(h.do("POST", page+"/revert", `{"versionId":"`+v1+`"}`, nil))
		v3, _ := reverted["versionId"].(string)
		if _, drifted := reverted["drifted"].([]any); v3 == "" || v3 == v1 || v3 == v2 || !drifted || len(reverted) != 2 {
			t.Fatalf("POST revert: %v", reverted)
		}
		head = ok(h.do("GET", page, "", nil))["head"].(map[string]any)
		blocks := head["blocks"].([]any)
		if head["id"] != v3 || head["parentVersionId"] != v2 || len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "The body." {
			t.Fatalf("the head after the revert: %v, want v1's two blocks over %s", head, v2)
		}
		if n := len(ok(h.do("GET", page+"/versions", "", nil))["versions"].([]any)); n != 3 {
			t.Fatalf("GET versions after the revert: %d, want 3 (a revert is a version)", n)
		}
		code, body = h.do("POST", page+"/revert", `{"versionId":"CV-none"}`, nil)
		refused(code, body, http.StatusNotFound, "NOT_FOUND")
		code, body = h.do("POST", page+"/revert", `{"versionId":"`+v1+`","bogus":1}`, nil)
		refused(code, body, http.StatusBadRequest, "VALIDATION")
		code, body = h.do("POST", page+"/revert", `{}`, nil)
		refused(code, body, http.StatusBadRequest, "VALIDATION")

		// A page is reached only through its own project: through another of the member's projects
		// it is absent on every route.
		elsewhere := "/api/projects/" + other + "/chronicle/" + pageID
		if v := ok(h.do("GET", elsewhere, "", nil)); v["page"] != nil || v["head"] != nil {
			t.Fatalf("GET the page through another project: %v, want page and head null", v)
		}
		if v := ok(h.do("GET", elsewhere+"/versions/"+v1, "", nil)); v["version"] != nil {
			t.Fatalf("GET a version through another project: %v, want null", v)
		}
		if n := len(ok(h.do("GET", elsewhere+"/versions", "", nil))["versions"].([]any)); n != 0 {
			t.Fatalf("GET versions through another project: %d, want 0", n)
		}
		code, body = h.do("POST", elsewhere+"/edit", `{"expectedParentVersionId":"`+v3+`","blocks":[]}`, nil)
		refused(code, body, http.StatusNotFound, "NOT_FOUND")
		code, body = h.do("POST", elsewhere+"/revert", `{"versionId":"`+v1+`"}`, nil)
		refused(code, body, http.StatusNotFound, "NOT_FOUND")
		code, body = h.do("DELETE", elsewhere, "", nil)
		refused(code, body, http.StatusNotFound, "NOT_FOUND")

		mallory := h.as("mallory")
		for _, r := range []struct{ method, path, body string }{
			{"GET", pc, ""}, {"GET", page, ""}, {"POST", pc, `{"kind":"adr","slug":"adr/x","title":"x","blocks":[]}`},
			{"DELETE", page, ""}, {"GET", page + "/versions", ""}, {"GET", page + "/versions/" + v1, ""},
			{"POST", page + "/edit", `{"expectedParentVersionId":"` + v3 + `","blocks":[]}`},
			{"POST", page + "/revert", `{"versionId":"` + v1 + `"}`},
		} {
			code, body := mallory.do(r.method, r.path, r.body, nil)
			var e struct{ Code string }
			_ = json.Unmarshal([]byte(body), &e)
			if code != http.StatusForbidden || e.Code != "FORBIDDEN" {
				t.Fatalf("%s %s as a non-member: %d %s, want 403 FORBIDDEN", r.method, r.path, code, body)
			}
		}

		// The confirmation pins the head it showed in the query string (deleteChroniclePage): a
		// head that moved is STALE and removes nothing.
		code, body = h.do("DELETE", page+"?expectedHeadVersionId="+v2, "", nil)
		refused(code, body, http.StatusConflict, "STALE")
		code, body = h.do("DELETE", page, `{"expectedHeadVersionId":"`+v3+`"}`, nil)
		refused(code, body, http.StatusBadRequest, "VALIDATION")
		if v := ok(h.do("GET", page, "", nil)); v["page"] == nil {
			t.Fatalf("a refused delete removed the page: %v", v)
		}
		if gone := ok(h.do("DELETE", page+"?expectedHeadVersionId="+v3, "", nil)); gone["ok"] != true {
			t.Fatalf("DELETE: %v", gone)
		}
		if v := ok(h.do("GET", page, "", nil)); v["page"] != nil {
			t.Fatalf("GET after the delete: %v, want page null", v)
		}
		if n := len(ok(h.do("GET", pc, "", nil))["pages"].([]any)); n != 0 {
			t.Fatalf("GET chronicle after the delete: %d pages, want 0", n)
		}
	})
}

// The index and the history page by cursor: a cursor the GET passes reaches the service, and
// nextCursor is present exactly while more records exist.
func TestChronicleRoutesPageByCursor(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, _ := seedBoard(t, h, s.Core, "Paged")
		ctx := t.Context()
		var pageID, head string
		for i := range 101 {
			out, err := s.Core.Chronicle().CreatePage(ctx, h.caller(), service.CreateChroniclePageReq{
				ProjectID: pid, Kind: "topic", Slug: "topic/p-" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Title: "p",
				Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: "x"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			pageID, head = out.PageID, out.VersionID
		}
		pc := "/api/projects/" + pid + "/chronicle"
		get := func(path string) map[string]any {
			t.Helper()
			code, body := h.do("GET", path, "", nil)
			var out map[string]any
			if err := json.Unmarshal([]byte(body), &out); err != nil || code != 200 {
				t.Fatalf("GET %s: %d %s", path, code, body)
			}
			return out
		}
		first := get(pc)
		cursor, _ := first["nextCursor"].(string)
		if len(first["pages"].([]any)) != 100 || cursor == "" {
			t.Fatalf("the first index page: %d pages, cursor %q; want 100 and a cursor", len(first["pages"].([]any)), cursor)
		}
		rest := get(pc + "?cursor=" + cursor)
		if _, more := rest["nextCursor"]; len(rest["pages"].([]any)) != 1 || more {
			t.Fatalf("the second index page: %v, want 1 page and no cursor", rest)
		}
		for range 50 {
			out, err := s.Core.Chronicle().EditPage(ctx, h.caller(), service.EditChroniclePageReq{
				ProjectID: pid, PageID: pageID, ExpectedParentVersionID: head, Blocks: []service.ChronicleBlockInput{{ID: "a-1", Text: head}},
			})
			if err != nil {
				t.Fatal(err)
			}
			head = out.VersionID
		}
		versions := pc + "/" + pageID + "/versions"
		older := get(versions)
		cursor, _ = older["nextCursor"].(string)
		if len(older["versions"].([]any)) != 50 || cursor == "" {
			t.Fatalf("the first history page: %d, cursor %q; want 50 and a cursor", len(older["versions"].([]any)), cursor)
		}
		last := get(versions + "?cursor=" + cursor)
		if _, more := last["nextCursor"]; len(last["versions"].([]any)) != 1 || more {
			t.Fatalf("the second history page: %v, want 1 version and no cursor", last)
		}
	})
}
