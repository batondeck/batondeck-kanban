package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The comment and follow-up routes (R2.1) over the real service on each engine servicetest selects:
// each route answers ui-backend's JSON for the signed-in member; the ack takes no body; a member of
// another project is refused 403 FORBIDDEN on every route and no session is 401.
func TestCommentAndFollowUpRoutes(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, bid := seedBoard(t, h, s.Core, "Notes")
		ctx := context.Background()
		task, err := s.Core.Tasks().Create(ctx, h.caller(), service.CreateTaskReq{ProjectID: pid, BoardID: bid, Title: "routed"})
		if err != nil {
			t.Fatal(err)
		}
		base := "/api/projects/" + pid + "/tasks/" + task.Task.ID
		decode := func(body string, into any) {
			t.Helper()
			if err := json.Unmarshal([]byte(body), into); err != nil {
				t.Fatalf("%v in %s", err, body)
			}
		}

		code, body := h.do("POST", base+"/comments", `{"body":"from the web"}`, nil)
		var added struct{ Comment service.Comment }
		decode(body, &added)
		if code != http.StatusOK || added.Comment.Body != "from the web" || added.Comment.Author != h.userID {
			t.Fatalf("POST comments: %d %s", code, body)
		}
		code, body = h.do("GET", base+"/comments?limit=10", "", nil)
		var listed struct{ Comments []service.Comment }
		decode(body, &listed)
		if code != http.StatusOK || len(listed.Comments) != 1 || listed.Comments[0].ID != added.Comment.ID {
			t.Fatalf("GET comments: %d %s", code, body)
		}

		code, body = h.do("POST", base+"/follow-ups", `{"body":"please review","reopen":false}`, nil)
		var fu struct {
			FollowUp service.FollowUp
			Task     service.Task
		}
		decode(body, &fu)
		if code != http.StatusOK || fu.FollowUp.Status != "open" || fu.Task.ID != task.Task.ID {
			t.Fatalf("POST follow-ups: %d %s", code, body)
		}
		if code, body := h.do("POST", base+"/follow-ups/"+fu.FollowUp.ID+"/ack", `{"followUpId":"FU-other"}`, nil); code != http.StatusBadRequest || !strings.Contains(body, `"VALIDATION"`) {
			t.Fatalf("an ack with a body: %d %s, want 400 VALIDATION", code, body)
		}
		code, body = h.do("POST", base+"/follow-ups/"+fu.FollowUp.ID+"/ack", "", nil)
		var acked struct{ FollowUp service.FollowUp }
		decode(body, &acked)
		if code != http.StatusOK || acked.FollowUp.Status != "acked" {
			t.Fatalf("POST ack: %d %s", code, body)
		}
		code, body = h.do("GET", base+"/follow-ups?openOnly=true", "", nil)
		var open struct{ FollowUps []service.FollowUp }
		decode(body, &open)
		if code != http.StatusOK || len(open.FollowUps) != 0 {
			t.Fatalf("GET follow-ups?openOnly=true after the ack: %d %s", code, body)
		}
		if code, body := h.do("GET", base+"/follow-ups", "", nil); code != http.StatusOK || !strings.Contains(body, `"status":"acked"`) {
			t.Fatalf("GET follow-ups: %d %s", code, body)
		}

		bob := h.as("bob")
		routes := [][3]string{
			{"GET", base + "/comments", ""}, {"POST", base + "/comments", `{"body":"x"}`},
			{"GET", base + "/follow-ups", ""}, {"POST", base + "/follow-ups", `{"body":"x"}`},
			{"POST", base + "/follow-ups/" + fu.FollowUp.ID + "/ack", ""},
		}
		for _, r := range routes {
			if code, body := bob.do(r[0], r[1], r[2], nil); code != http.StatusForbidden || !strings.Contains(body, `"FORBIDDEN"`) {
				t.Errorf("%s %s as a non-member: %d %s, want 403 FORBIDDEN", r[0], r[1], code, body)
			}
			req := h.request(r[0], r[1], r[2], nil)
			req.Header.Del("Cookie")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with no session: %d, want 401", r[0], r[1], resp.StatusCode)
			}
		}
		t.Logf("%s: 5 routes answered the member, refused a non-member 403 and no session 401", s.Engine)
	})
}
