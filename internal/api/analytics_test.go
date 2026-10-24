package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
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
		// Three calls through the recorder the MCP middleware uses, flushed to the store.
		for _, r := range []service.RecordToolCallReq{
			{Tool: "claim_next", Outcome: "ok", Code: "OK", LatencyMs: 8, ProjectID: p.ProjectID},
			{Tool: "claim_next", Outcome: "ok", Code: "OK", LatencyMs: 8, ProjectID: p.ProjectID},
			{Tool: "claim_next", Outcome: "error", Code: "STALE", LatencyMs: 8, ProjectID: p.ProjectID},
		} {
			if _, err := s.Services.ToolCalls.Record(ctx, h.caller(), r); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Core.FlushToolCalls(ctx); err != nil {
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
