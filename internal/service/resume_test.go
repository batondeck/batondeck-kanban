package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func strp(s string) *string { return &s }

func checkpointText(cp *Checkpoint) map[string]any {
	if cp == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range map[string]*string{"DONE": cp.DONE, "NEXT": cp.NEXT, "REJECTED": cp.REJECTED, "UNCERTAIN": cp.UNCERTAIN} {
		if v == nil {
			out[k] = nil
		} else {
			out[k] = *v
		}
	}
	return out
}

// TestResumeParseCheckpoint (P-117): the four headings in each form the skill's agents write, a
// section's text to the next heading, an absent or empty section null, and a note with no heading
// no checkpoint at all.
func TestResumeParseCheckpoint(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       map[string]any
	}{
		{"bare with colons", "DONE: tests pass (go test ./x)\nNEXT: wire the handler\nREJECTED: a cache, too slow\nUNCERTAIN: none",
			map[string]any{"DONE": "tests pass (go test ./x)", "NEXT": "wire the handler", "REJECTED": "a cache, too slow", "UNCERTAIN": "none"}},
		{"markdown headings, multi-line", "Checkpoint 2\n\n## DONE\n- parser\n- store\n\n## NEXT\nthe UI\n\n## REJECTED\n\n## UNCERTAIN\n",
			map[string]any{"DONE": "- parser\n- store", "NEXT": "the UI", "REJECTED": nil, "UNCERTAIN": nil}},
		{"bold and bullets", "**DONE:** one\n- **NEXT**: two\n* UNCERTAIN: three\r\n",
			map[string]any{"DONE": "one", "NEXT": "two", "REJECTED": nil, "UNCERTAIN": "three"}},
		{"first non-empty of a repeated heading", "NEXT: first\nNEXT: second", map[string]any{"DONE": nil, "NEXT": "first", "REJECTED": nil, "UNCERTAIN": nil}},
		{"no heading", "NEXT steps are unclear; DONE is a word here.\nnext: lower case", nil},
		{"heading word inside a line", "We are DONE: almost", nil},
	} {
		got := checkpointText(parseCheckpoint(c.body))
		if (got == nil) != (c.want == nil) || len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %#v, want %#v", c.name, k, got[k], v)
			}
		}
	}
}

// TestResumeDerivation (P-117): the newest live branch and commit win, a retracted one is never
// reported, a ref is preferred to a url; the newest note holding a heading is the checkpoint, a
// note with none and a decision skipped; resume is nil only when nothing is there.
func TestResumeDerivation(t *testing.T) {
	retracted := &store.ArtifactRetraction{By: "u-ada", At: 9, Reason: "wrong branch"}
	arts := []store.Artifact{
		{Kind: store.ArtifactBranch, Ref: strp("feat/old"), TS: 1},
		{Kind: store.ArtifactBranch, Ref: strp("feat/new"), TS: 2},
		{Kind: store.ArtifactBranch, Ref: strp("feat/withdrawn"), TS: 3, Retraction: retracted},
		{Kind: store.ArtifactCommit, URL: strp("https://git.example/c/abc123"), TS: 1},
		{Kind: store.ArtifactPR, URL: strp("https://git.example/pr/1"), TS: 4},
	}
	items := []store.ContextItem{ // newest first, as ListContextItems serves them
		{Kind: store.ContextNote, Body: "just a note", TS: 5},
		{Kind: store.ContextDecision, Body: "DONE: a decision is no checkpoint", TS: 4},
		{Kind: store.ContextNote, Body: "DONE: newer\nNEXT: go on", TS: 3},
		{Kind: store.ContextNote, Body: "DONE: older", TS: 2},
	}
	r := resumeFrom(arts, lastCheckpoint(items))
	if r == nil || r.Branch == nil || *r.Branch != "feat/new" || r.HeadCommit == nil || *r.HeadCommit != "https://git.example/c/abc123" {
		t.Fatalf("resume %+v, want branch feat/new and the commit's url", r)
	}
	if cp := checkpointText(r.LastCheckpoint); cp["DONE"] != "newer" || cp["NEXT"] != "go on" || cp["REJECTED"] != nil {
		t.Fatalf("lastCheckpoint %v, want the newer note's", cp)
	}
	if r := resumeFrom(arts[2:3], lastCheckpoint(items[:2])); r != nil {
		t.Fatalf("only a retracted branch and no checkpoint: resume %+v, want nil", r)
	}
	if r := resumeFrom(nil, nil); r != nil {
		t.Fatalf("an empty task: resume %+v, want nil", r)
	}
	if r := resumeFrom(nil, lastCheckpoint(items[3:])); r == nil || r.Branch != nil || r.HeadCommit != nil || r.LastCheckpoint == nil {
		t.Fatalf("a checkpoint alone: resume %+v, want it with null branch and commit", r)
	}
}

// TestResumeCheckpointWindow (P-117 review B4): the checkpoint is sought among the task's newest
// 200 context items, as get_task_context's description says: behind 199 newer items it
// is found; behind 200 it is not, and a task with no branch or commit answers null.
func TestResumeCheckpointWindow(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Todo, nil)
	add := func(body string) {
		t.Helper()
		if _, err := f.st.AddContextItem(f.ctx, store.AddContextItemInput{ProjectID: f.project.ID, TaskID: task.ID, Kind: store.ContextNote, Body: body, Author: "alice"}); err != nil {
			t.Fatal(err)
		}
	}
	add("DONE: the first half\nNEXT: the second half")
	time.Sleep(2 * time.Millisecond) // strictly older than every note after it
	for i := 0; i < 199; i++ {
		add("a plain note")
	}
	r, err := f.c.resumeOf(f.ctx, f.project.ID, task.ID)
	if err != nil || r == nil || r.LastCheckpoint == nil || r.LastCheckpoint.DONE == nil || *r.LastCheckpoint.DONE != "the first half" {
		t.Fatalf("behind 199 newer items: resume %+v, %v, want the checkpoint", r, err)
	}
	add("a plain note")
	if r, err := f.c.resumeOf(f.ctx, f.project.ID, task.ID); err != nil || r != nil {
		t.Fatalf("behind 200 newer items: resume %+v, %v, want null", r, err)
	}
}
