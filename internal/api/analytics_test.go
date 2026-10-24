package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// GET /api/analytics/operations on each engine, through the real login: a member gets the summary
// of the project's rows with days 365 clamped to 90 and the 45-day retention row; a non-member gets
// 403 FORBIDDEN, never an empty summary; no projectId is 400 VALIDATION.
func TestAnalyticsOperationsRoute(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		ctx := context.Background()
		p, err := s.Services.Projects.Create(ctx, h.caller(), service.CreateProjectReq{Name: "ops"})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UnixMilli()
		call := func(outcome store.ToolCallOutcome, code string) store.ToolCall {
			return store.ToolCall{TS: now, Tool: "claim_next", Identity: h.userID, Agent: "claude", Outcome: outcome, Code: code, LatencyMs: 8, ProjectID: p.ProjectID}
		}
		if err := s.Store.InsertToolCalls(ctx, []store.ToolCall{call(store.ToolCallOK, "OK"), call(store.ToolCallOK, "OK"), call(store.ToolCallError, "STALE")}); err != nil {
			t.Fatal(err)
		}
		path := "/api/analytics/operations?projectId=" + p.ProjectID + "&days=365"

		code, body := h.do("GET", path, "", nil)
		if code != http.StatusOK {
			t.Fatalf("member: %d %s, want 200", code, body)
		}
		var got service.OperationsResp
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if got.ProjectID != p.ProjectID || got.Days != 90 || got.Total != 3 || got.Errors != 1 || got.Retention.Days != 45 || got.Retention.OldestDay == "" {
			t.Fatalf("member: %s, want days 90, total 3, errors 1, retention 45 days", body)
		}

		if code, body := h.as("mallory").do("GET", path, "", nil); code != http.StatusForbidden || codeOf(t, body) != "FORBIDDEN" {
			t.Fatalf("non-member: %d %s, want 403 FORBIDDEN", code, body)
		}
		if code, body := h.do("GET", "/api/analytics/operations?days=7", "", nil); code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" {
			t.Fatalf("no projectId: %d %s, want 400 VALIDATION", code, body)
		}
	})
}
