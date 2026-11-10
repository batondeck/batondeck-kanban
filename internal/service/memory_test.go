package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// An oversized write_memory value is told what works: no refusal points at largeArtifact, which is
// itself refused, so the two refusals never send a caller back and forth.
func TestWriteMemoryOversizeNamesAWayThatWorks(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Todo, nil)
	cx := f.c.Context()
	big := strings.Repeat("x", 40_000)
	_, err := cx.WriteMemory(f.ctx, f.alice, WriteMemoryReq{ProjectID: f.project.ID, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "big", Value: big})
	e := wantCode(t, err, domain.CodeValidation)
	if strings.Contains(e.Message, "largeArtifact") || !strings.Contains(e.Message, "attach_file") {
		t.Fatalf("oversize: %q, want attach_file named and largeArtifact not", e.Message)
	}
	_, err = cx.WriteMemory(f.ctx, f.alice, WriteMemoryReq{ProjectID: f.project.ID, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "big",
		LargeArtifact: LargeArtifact{FileName: "big.txt", MimeType: "text/plain"}})
	e = wantCode(t, err, domain.CodeValidation)
	if !strings.Contains(e.Message, "attach_file") {
		t.Fatalf("largeArtifact: %q, want attach_file named", e.Message)
	}
}

// A memory.written event names the key of a shared entry, but not of a private one (agent, visible
// only to its author; agent_global, private to it): the task's history and the board feed are every
// member's, so a private key there would publish what the note is about.
func TestMemoryWrittenEventsKeepPrivateKeysPrivate(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Todo, nil)
	cx := f.c.Context()
	for _, w := range []struct {
		scope domain.MemoryScope
		key   string
	}{{domain.MemoryScopeShared, "handoff"}, {domain.MemoryScopeAgent, "customer-acme-churn-risk"}, {domain.MemoryScopeAgentGlobal, "my-global-pref"}} {
		if _, err := cx.WriteMemory(f.ctx, f.alice, WriteMemoryReq{ProjectID: f.project.ID, TaskID: task.ID, Scope: w.scope, Key: w.key, Value: "v"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.st.ListTaskEvents(f.ctx, f.project.ID, task.ID, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range page.Events {
		if ev.Type == domain.EventMemoryWritten {
			got = append(got, string(ev.Data))
		}
	}
	slices.Sort(got)
	want := []string{`{"key":"handoff","scope":"shared"}`, `{"scope":"agent"}`, `{"scope":"agent_global"}`}
	if !slices.Equal(got, want) {
		t.Fatalf("memory.written data %v, want %v", got, want)
	}
}
