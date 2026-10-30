package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// /api/session names the signed-in account (P-314): identity is an opaque `U-<hex>` id, and an
// account in no project is in no project's memberDisplay, so the SPA's identity chip has no other
// read to name it from. The display name when the account has one, else its login; never the id.
// The login fallback is auth.Accounts.Name's, tested in internal/auth (TestAccountName), since a
// transport's tests may not write a store row (ADR-0003 §C row 6).
func TestSessionDisplayName(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		session := func(h *harness) map[string]any {
			t.Helper()
			code, body := h.do("GET", "/api/session", "", nil)
			if code != http.StatusOK {
				t.Fatalf("session: %d %s", code, body)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}

		// alice was added with the display name "Alice" and belongs to no project.
		got := session(h)
		if got["identity"] != h.userID || got["displayName"] != "Alice" {
			t.Fatalf("alice's session: identity %v displayName %v, want %s and Alice", got["identity"], got["displayName"], h.userID)
		}
	})
}
