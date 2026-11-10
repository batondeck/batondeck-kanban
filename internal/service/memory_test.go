package service

import (
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
