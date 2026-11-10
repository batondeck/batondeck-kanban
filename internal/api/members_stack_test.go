package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The onboarding order the Settings page invites: a project admin adds a teammate by login before
// a workspace admin has made the account. add_member keeps that login as the member key. Once the
// account exists, the Remove button (which sends the row's key) removes that row and not the
// account's own membership, and adding the login again folds the row into the account.
func TestAMemberAddedByLoginBeforeItsAccountOverREST(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, _ := seedBoard(t, h, s.Core, "Onboarding")
		members := func() map[string]string {
			t.Helper()
			code, body := h.do("GET", "/api/projects/"+pid, "", nil)
			var got struct {
				Project struct{ Members map[string]string }
			}
			if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil {
				t.Fatalf("GET project: %d %s", code, body)
			}
			return got.Project.Members
		}
		if code, body := h.do("POST", "/api/projects/"+pid+"/members", `{"identityId":"carol@example.com","role":"member"}`, nil); code != http.StatusOK {
			t.Fatalf("POST members: %d %s", code, body)
		}
		carol, err := h.auth.Accounts.Add(context.Background(), "carol@example.com", "Carol", testPassword, false)
		if err != nil {
			t.Fatal(err)
		}
		if code, body := h.do("POST", "/api/projects/"+pid+"/members", `{"identityId":"carol@example.com","role":"member"}`, nil); code != http.StatusOK {
			t.Fatalf("POST members again: %d %s", code, body)
		}
		if m := members(); len(m) != 2 || m[carol.ID] != "member" {
			t.Fatalf("members after adding the login again %v, want alice and %s only", m, carol.ID)
		}
		// A login row left from before (added again only after the account existed, here by id).
		if code, body := h.do("POST", "/api/projects/"+pid+"/members", `{"identityId":"dave@example.com","role":"member"}`, nil); code != http.StatusOK {
			t.Fatalf("POST members: %d %s", code, body)
		}
		if _, err := h.auth.Accounts.Add(context.Background(), "dave@example.com", "Dave", testPassword, false); err != nil {
			t.Fatal(err)
		}
		code, body := h.do("DELETE", "/api/projects/"+pid+"/members/dave%40example.com", "", nil)
		t.Logf("DELETE the login row after the account exists: %d %s", code, body)
		if code != http.StatusOK {
			t.Fatalf("DELETE members/dave@example.com: %d %s", code, body)
		}
		if m := members(); len(m) != 2 || m[carol.ID] != "member" {
			t.Fatalf("members after removing the login row %v, want alice and %s", m, carol.ID)
		}
	})
}
