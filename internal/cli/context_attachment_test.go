package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// TestTaskContextJoinsReadyAttachment (P-236 AC1): through the composed server, an attachment
// whose bytes were PUT to attach_file's URL is ready in get_task_context with the bytes' sha256
// and its blob: locator, and no download URL (list_attachments signs those).
func TestTaskContextJoinsReadyAttachment(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			s := startBlobs(t, e.open(t), 16)
			taskID := s.task(t)
			body := []byte("hello")
			if code, got := do(t, http.MethodPut, s.attach(t, taskID, len(body)), bytes.NewReader(body), int64(len(body))); code != http.StatusOK {
				t.Fatalf("PUT the upload URL: %d %s", code, got)
			}
			out, ok := s.tool(t, "get_task_context", map[string]any{"projectId": s.project, "taskId": taskID})
			if !ok {
				t.Fatalf("get_task_context: %v", out)
			}
			atts, _ := out["attachments"].([]any)
			sum := sha256.Sum256(body)
			if len(atts) != 1 {
				t.Fatalf("attachments = %v, want one", out["attachments"])
			}
			a := atts[0].(map[string]any)
			if a["status"] != "ready" || a["sha256"] != hex.EncodeToString(sum[:]) || !strings.HasPrefix(a["gcsUri"].(string), "blob:") {
				t.Fatalf("the ready attachment = %v, want sha256 %x", a, sum)
			}
			if _, ok := a["downloadUrl"]; ok {
				t.Fatalf("the brief carries a downloadUrl: %v", a)
			}
			t.Logf("%s get_task_context: %s %s sha256 %s", e.name, a["status"], a["gcsUri"], a["sha256"])
		})
	}
}
