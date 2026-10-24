package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The race and sprint routes on each engine: a member's session lists, starts, opens, cancels and
// reads; no session is 401 before any service; a member of nothing of the project's is 403
// FORBIDDEN (the positive control is the member's 200 on the same path).
func TestRunAndSprintRoutesOnTheStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, bid := seedBoard(t, h, s.Core, "Races")
		ctx := context.Background()
		task, err := s.Core.Tasks().Create(ctx, h.caller(), service.CreateTaskReq{ProjectID: pid, BoardID: bid, Title: "raced"})
		if err != nil {
			t.Fatal(err)
		}
		tid := task.Task.ID
		runs := "/api/projects/" + pid + "/tasks/" + tid + "/runs"
		decode := func(code int, body string) map[string]any {
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
		started := decode(h.do("POST", runs, `{"version":1,"count":2}`, nil))
		if n := len(started["runs"].([]any)); n != 2 {
			t.Fatalf("POST runs: %d runs, want 2", n)
		}
		decode(h.do("POST", runs+"/open", `{}`, nil))
		listed := decode(h.do("GET", runs, "", nil))
		if n := len(listed["runs"].([]any)); n != 3 {
			t.Fatalf("GET runs: %d, want 3", n)
		}
		parent := started["task"].(map[string]any)
		back := decode(h.do("POST", runs+"/cancel", `{"version":`+jsonNum(parent["version"])+`}`, nil))
		if p := back["task"].(map[string]any); p["status"] != "TODO" || p["racing"] == true {
			t.Fatalf("POST runs/cancel: %v", p)
		}
		if n := len(decode(h.do("GET", runs, "", nil))["runs"].([]any)); n != 0 {
			t.Fatalf("GET runs after cancel: %d, want 0", n)
		}

		sp, err := s.Core.Sprints().Create(ctx, h.caller(), service.CreateSprintReq{ProjectID: pid, BoardID: bid, Name: "S", Goal: "g", Acceptance: "a"})
		if err != nil {
			t.Fatal(err)
		}
		sprints := "/api/projects/" + pid + "/sprints"
		if n := len(decode(h.do("GET", sprints+"?boardId="+bid+"&status=PROPOSED", "", nil))["sprints"].([]any)); n != 1 {
			t.Fatalf("GET sprints: %d, want 1", n)
		}
		got := decode(h.do("GET", sprints+"/"+sp.Sprint.ID, "", nil))
		if got["sprint"].(map[string]any)["id"] != sp.Sprint.ID {
			t.Fatalf("GET sprint: %v", got)
		}
		upd := decode(h.do("POST", sprints+"/"+sp.Sprint.ID, `{"version":1,"patch":{"status":"ABANDONED"}}`, nil))
		if upd["sprint"].(map[string]any)["status"] != "ABANDONED" {
			t.Fatalf("POST sprint: %v", upd)
		}

		req := h.request("GET", runs, "", nil)
		req.Header.Del("Cookie")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET runs with no session: %d, want 401", resp.StatusCode)
		}
		mallory := h.as("mallory")
		for _, r := range []struct{ method, path, body string }{
			{"GET", runs, ""}, {"POST", runs, `{"version":1}`}, {"POST", runs + "/pick", `{"version":1,"runTaskId":"T-2"}`},
			{"GET", sprints, ""}, {"GET", sprints + "/" + sp.Sprint.ID, ""},
		} {
			code, body := mallory.do(r.method, r.path, r.body, nil)
			var e struct{ Code string }
			_ = json.Unmarshal([]byte(body), &e)
			if code != http.StatusForbidden || e.Code != "FORBIDDEN" {
				t.Fatalf("%s %s as a non-member: %d %s, want 403 FORBIDDEN", r.method, r.path, code, body)
			}
		}
	})
}

func jsonNum(v any) string { b, _ := json.Marshal(v); return string(b) }
