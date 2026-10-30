package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// /api/session names the signed-in account (P-314): identity is an opaque `U-<hex>` id, and an
// account in no project is in no project's memberDisplay, so the SPA's identity chip has no other
// read to name it from. The display name when the account has one, else its login; never the id.
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

		// An account row with no display name (one created before names were required) is named by
		// its login. Written through the store, since Accounts.Add always fills the name.
		hash, err := auth.HashPassword(testPassword, fastArgon)
		if err != nil {
			t.Fatal(err)
		}
		const id = "U-0000000000000b0b"
		if err := s.Store.CreateUser(context.Background(), store.User{ID: id, Email: "bob", PasswordHash: hash, CreatedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		bob := &harness{t: t, auth: h.auth, srv: h.srv, userID: id}
		bob.login("bob", testPassword)
		got = session(bob)
		if got["identity"] != id || got["displayName"] != "bob" {
			t.Fatalf("bob's session: identity %v displayName %v, want %s and bob", got["identity"], got["displayName"], id)
		}
	})
}
