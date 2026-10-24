package service

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// analytics is service.Analytics over store.ToolCalls.SummarizeToolCalls.
type analytics struct{ c *Core }

var _ Analytics = analytics{}

// Analytics is the operations summary service.
func (c *Core) Analytics() Analytics { return analytics{c} }

// The window conductor's ui-backend asked for by default (OPS_DEFAULT_DAYS) and the plan's ceiling.
const (
	defaultOpsDays = 7
	maxOpsDays     = 90
)

// analyticsTool is the posture the summary is authorized under: conductor's ui-backend gated the
// route on get_project (app.ts:2378).
const analyticsTool = "get_project"

func (a analytics) Operations(ctx context.Context, caller domain.Caller, req OperationsReq) (resp OperationsResp, err error) {
	defer a.c.recovered("analytics_operations", &err)
	if _, err := a.c.authorize(ctx, caller, req.ProjectID, analyticsTool, domain.GrantSubject{}, false); err != nil {
		return resp, err
	}
	// Clamped rather than refused, as conductor's effectiveDays does; the response names the window.
	days := req.Days
	if days < 1 {
		days = defaultOpsDays
	}
	days = min(days, maxOpsDays)
	now := a.c.now().UTC()
	since := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	groups, err := a.c.store.SummarizeToolCalls(ctx, req.ProjectID, since)
	if err != nil {
		return resp, a.c.mapErr("analytics_operations", err)
	}
	resp = OperationsResp{ProjectID: req.ProjectID, Days: days, ByTool: []OpsToolRow{}, ByDay: []OpsDayRow{}, ByAgent: []OpsAgentRow{},
		Retention: OpsRetentionRow{Days: a.c.toolCallRetentionDays,
			OldestDay: now.Add(-time.Duration(a.c.toolCallRetentionDays) * 24 * time.Hour).Format(time.DateOnly)}}
	// aggregateOps (bq.ts:199): totals, then per tool, per day and per agent.
	type toolAcc struct{ calls, latency int64 }
	tools, byDay, byAgent := map[string]*toolAcc{}, map[string]int64{}, map[string]int64{}
	var latency int64
	for _, g := range groups {
		resp.Total += g.Calls
		if g.Outcome == "error" {
			resp.Errors += g.Calls
		}
		latency += g.LatencySum
		t := tools[g.Tool]
		if t == nil {
			t = &toolAcc{}
			tools[g.Tool] = t
		}
		t.calls += g.Calls
		t.latency += g.LatencySum
		byDay[g.Day] += g.Calls
		byAgent[cmp.Or(g.Agent, "(unknown)")] += g.Calls
	}
	if resp.Total > 0 {
		resp.ErrorRate = float64(resp.Errors) / float64(resp.Total)
		resp.AvgLatencyMs = tenth(latency, resp.Total)
	}
	for tool, t := range tools {
		resp.ByTool = append(resp.ByTool, OpsToolRow{Tool: tool, Calls: t.calls, AvgMs: tenth(t.latency, t.calls)})
	}
	for day, n := range byDay {
		resp.ByDay = append(resp.ByDay, OpsDayRow{Day: day, Calls: n})
	}
	for agent, n := range byAgent {
		resp.ByAgent = append(resp.ByAgent, OpsAgentRow{Agent: agent, Calls: n})
	}
	// Conductor's sorts, with a name tiebreak so equal counts do not reorder between calls.
	slices.SortFunc(resp.ByTool, func(x, y OpsToolRow) int { return cmp.Or(cmp.Compare(y.Calls, x.Calls), cmp.Compare(x.Tool, y.Tool)) })
	slices.SortFunc(resp.ByDay, func(x, y OpsDayRow) int { return cmp.Compare(x.Day, y.Day) })
	slices.SortFunc(resp.ByAgent, func(x, y OpsAgentRow) int {
		return cmp.Or(cmp.Compare(y.Calls, x.Calls), cmp.Compare(x.Agent, y.Agent))
	})
	return resp, nil
}

// tenth is sum/n rounded to 0.1, conductor's Math.round(x * 10) / 10.
func tenth(sum, n int64) float64 {
	return math.Round(float64(sum)/float64(n)*10) / 10
}
