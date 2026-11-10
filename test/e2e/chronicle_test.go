//go:build e2e

package e2e

import (
	"testing"
)

func init() {
	register(scenario{area: "chronicle", name: "create-edit-delete", tools: []string{"create_chronicle_page", "edit_chronicle_page", "delete_chronicle_page"}, run: chronicleCreateEditDelete})
	register(scenario{area: "chronicle", name: "ingest", tools: []string{"ingest_chronicle_page"}, run: chronicleIngest})
}

// blocks is a page body: id, text pairs.
func blocks(pairs ...string) []any {
	var out []any
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, M{"id": pairs[i], "text": pairs[i+1]})
	}
	return out
}

// page reads a chronicle page through its resource and answers its head's block texts by id.
func page(t *testing.T, c *Client, project, pageID string) (M, map[string]string) {
	t.Helper()
	out, rerr := c.Read(t, "batondeck://"+project+"/chronicle/"+pageID)
	if rerr != nil {
		t.Fatalf("reading chronicle page %s: %v", pageID, rerr)
	}
	texts := map[string]string{}
	for _, b := range A(out, "head.blocks") {
		texts[S(b, "id")] = S(b, "text")
	}
	return out, texts
}

// The worker starts a record by hand and edits it from its head; each version reads back through
// the page resource. A taken slug, a sweep-shaped block id, an edit from a moved head and a viewer's
// write are refused. Deleting is the master's (the owner's ruling of 2026-11-10): a worker's delete is
// FORBIDDEN, a delete from a stale head is STALE, and a delete leaves no page and one audit row.
func chronicleCreateEditDelete(t *testing.T, e *env) {
	p := e.project(t, "Chronicle")
	slug := "adr/parser-" + uniq("s")[2:]
	made := e.worker.Call(t, "create_chronicle_page", M{"projectId": p.id, "kind": "adr", "slug": slug, "title": "Use a hand-written parser",
		"blocks": blocks("ctx", "The feed grammar is small.", "decision", "Write the parser by hand.")})
	pid, v1 := S(made, "pageId"), S(made, "versionId")
	want(t, pid != "" && v1 != "", "create_chronicle_page answered %s", js(made))
	got, texts := page(t, e.viewer, p.id, pid)
	want(t, S(got, "page.slug") == slug && S(got, "page.headVersionId") == v1 && texts["decision"] == "Write the parser by hand.", "the page after create: %s", js(got))

	e.worker.Refuse(t, "create_chronicle_page", M{"projectId": p.id, "kind": "adr", "slug": slug, "title": "again", "blocks": blocks("a", "b")}, "VALIDATION")
	e.worker.Refuse(t, "create_chronicle_page", M{"projectId": p.id, "kind": "adr", "slug": slug + "-x", "title": "sweep id", "blocks": blocks("dec1/2", "b")}, "VALIDATION")
	e.viewer.Refuse(t, "create_chronicle_page", M{"projectId": p.id, "kind": "topic", "slug": slug + "-v", "title": "viewer", "blocks": blocks("a", "b")}, "FORBIDDEN")

	ed := e.worker.Call(t, "edit_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedParentVersionId": v1,
		"blocks": blocks("ctx", "The feed grammar is small and stable.", "decision", "Write the parser by hand.", "consequence", "We own the error messages.")})
	v2 := S(ed, "versionId")
	want(t, v2 != "" && v2 != v1, "edit_chronicle_page answered %s", js(ed))
	got, texts = page(t, e.viewer, p.id, pid)
	want(t, S(got, "page.headVersionId") == v2 && texts["consequence"] == "We own the error messages." && S(got, "head.parentVersionId") == v1, "the page after the edit: %s", js(got))
	e.master.Refuse(t, "edit_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedParentVersionId": v1, "blocks": blocks("ctx", "lost update")}, "STALE")
	e.viewer.Refuse(t, "edit_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedParentVersionId": v2, "blocks": blocks("ctx", "x")}, "FORBIDDEN")

	e.worker.Refuse(t, "delete_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedHeadVersionId": v2}, "FORBIDDEN")
	e.viewer.Refuse(t, "delete_chronicle_page", M{"projectId": p.id, "pageId": pid}, "FORBIDDEN")
	e.master.Refuse(t, "delete_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedHeadVersionId": v1}, "STALE")
	del := e.master.Call(t, "delete_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedHeadVersionId": v2})
	want(t, B(del, "ok"), "delete_chronicle_page answered %s", js(del))
	// The page leaves no version behind; the audit log names who deleted it, in which project.
	rows := A(e.admin.Call(t, "list_audit_log", M{"action": "chronicle.page_deleted", "subject": pid}), "entries")
	want(t, len(rows) == 1 && S(rows[0], "actor") == e.srv.ids["master"] && S(rows[0], "projectId") == p.id && S(rows[0], "agent") == "e2e-master",
		"list_audit_log chronicle.page_deleted for %s: %s", pid, js(rows))
	gone, _ := page(t, e.viewer, p.id, pid)
	want(t, gone["page"] == nil && gone["head"] == nil, "the deleted page still reads: %s", js(gone))
	idx, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle")
	want(t, rerr == nil && !has(ids(A(idx, "pages"), "id"), pid), "the chronicle index still lists the deleted page: %v %s", rerr, js(idx))
}

// A generated record is ingested (created), a re-ingest of the same blocks writes no version, a
// human edit is merged with a new ingest, and a block both sides changed comes back as a conflict.
// The viewer may not ingest and an unknown kind is refused.
func chronicleIngest(t *testing.T, e *env) {
	p := e.project(t, "Ingest")
	slug := "feature/feed-" + uniq("s")[2:]
	args := func(b []any) M {
		return M{"projectId": p.id, "kind": "feature", "slug": slug, "title": "Feed ingestion", "sourcePath": "docs/feed.md", "coverage": "full", "topics": []any{"feed"}, "blocks": b}
	}
	first := e.worker.Call(t, "ingest_chronicle_page", args(blocks("summary", "Feeds arrive hourly.", "limits", "At most 10 MB.")))
	pid, v1 := S(first, "pageId"), S(first, "versionId")
	want(t, B(first, "created") && !B(first, "unchanged") && pid != "", "the first ingest answered %s", js(first))
	same := e.worker.Call(t, "ingest_chronicle_page", args(blocks("summary", "Feeds arrive hourly.", "limits", "At most 10 MB.")))
	want(t, !B(same, "created") && B(same, "unchanged") && S(same, "versionId") == v1, "an unchanged re-ingest answered %s", js(same))
	hist, rerr := e.viewer.Read(t, "batondeck://"+p.id+"/chronicle/"+pid+"/versions")
	want(t, rerr == nil && len(A(hist, "versions")) == 1, "the history after an unchanged re-ingest: %v %s", rerr, js(hist))

	// A human edits the summary; the next ingest changes the limits (merged) and the summary (both
	// changed: a conflict).
	e.master.Call(t, "edit_chronicle_page", M{"projectId": p.id, "pageId": pid, "expectedParentVersionId": v1, "blocks": blocks("summary", "Feeds arrive hourly, in UTC.", "limits", "At most 10 MB.")})
	merged := e.worker.Call(t, "ingest_chronicle_page", args(blocks("summary", "Feeds arrive every 30 minutes.", "limits", "At most 20 MB.")))
	want(t, !B(merged, "created") && has(strs(A(merged, "conflicts")), "summary") && !has(strs(A(merged, "conflicts")), "limits"), "the merging ingest answered %s", js(merged))
	got, texts := page(t, e.viewer, p.id, pid)
	want(t, texts["limits"] == "At most 20 MB." && texts["summary"] == "Feeds arrive hourly, in UTC.", "the page after the merge keeps the human summary and takes the new limits: %s", js(got))

	e.viewer.Refuse(t, "ingest_chronicle_page", args(blocks("summary", "x")), "FORBIDDEN")
	bad := args(blocks("summary", "x"))
	bad["kind"] = "novel"
	e.worker.Refuse(t, "ingest_chronicle_page", bad, "VALIDATION")
}
