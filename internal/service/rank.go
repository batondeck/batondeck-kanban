package service

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Ranking is the scorer and fan-out service.
func (c *Core) Ranking() Ranking { return rankingSvc{c} }

type rankingSvc struct{ c *Core }

const (
	defaultRankLimit = 20 // rank_tasks's limit when none is given (conductor's args.limit ?? 20)
	maxChildren      = 50 // orchestrate_subtasks's children: 1 to 50
	// nearTie is the score difference under which the profile path breaks ties by tier, then by
	// reliability (conductor's NEAR_TIE_EPS).
	nearTie = 1e-3
)

type ranked struct {
	task        store.Task
	score       domain.TaskScore
	tier        domain.MatchTier
	reliability float64
}

func scoreInput(t store.Task) domain.ScoreInput {
	in := domain.ScoreInput{Priority: t.Priority, EstimateHours: t.Estimate, Blocks: len(t.Blocks), RequiredCapabilities: t.RequiredCapabilities,
		CreatedAt: time.UnixMilli(t.CreatedAt)}
	if t.DueAt != nil {
		d := time.UnixMilli(*t.DueAt)
		in.DueAt = &d
	}
	return in
}

func factorOut(f domain.ScoreFactor) AgingFactor {
	return AgingFactor{Contribution: f.Contribution, Value: f.Value, Weight: f.Weight}
}

// Rank is rank_tasks: the pool (at most five pages), and on the profile path the profile and its
// stats.
func (s rankingSvc) Rank(ctx context.Context, caller domain.Caller, req RankTasksReq) (RankTasksResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "rank_tasks", domain.GrantSubject{}, false); err != nil {
		return RankTasksResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return RankTasksResp{}, err
	}
	if err := checkLabelList("capabilities", req.Capabilities); err != nil {
		return RankTasksResp{}, err
	}
	if req.Assignee != "" {
		if err := checkLabel("assignee", req.Assignee); err != nil {
			return RankTasksResp{}, err
		}
	}
	if req.Limit < 0 || req.Limit > listMaxLimit {
		return RankTasksResp{}, domain.Validation("limit is 1 to 200", map[string]any{"field": "limit"})
	}
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultRankLimit
	}
	proj, err := s.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
	}
	todo := domain.Todo
	f := store.TaskFilter{BoardID: req.BoardID, Status: &todo}
	if req.Assignee != "" {
		a := req.Assignee
		f.Assignee = &a
	}
	tasks, err := boardTasks(ctx, s.c.store, req.ProjectID, f)
	if err != nil {
		return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
	}
	now := s.c.now()
	var pool []store.Task
	for _, t := range tasks {
		if domain.IsClaimable(claimView(t), now) && (req.SprintID == "" || slices.Contains(t.SprintIDs, req.SprintID)) {
			pool = append(pool, t)
		}
	}
	w := domain.SelectionWeights{Priority: settings.SelectionWeights.Priority, Readiness: settings.SelectionWeights.Readiness,
		Deadline: settings.SelectionWeights.Deadline, Aging: settings.SelectionWeights.Aging, Capability: settings.SelectionWeights.Capability}
	ceiling := time.Duration(settings.AgingCeilingMs) * time.Millisecond

	var out []ranked
	profiled := false
	if req.UseProfile {
		key, err := agentKey(caller)
		if err != nil {
			return RankTasksResp{}, err
		}
		p, err := s.c.store.GetAgentProfile(ctx, req.ProjectID, key)
		switch {
		case err == nil:
			profiled = true
			stats, err := s.c.store.ListSkillStats(ctx, req.ProjectID, key)
			if err != nil {
				return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
			}
			byCap := map[string]store.SkillStat{}
			for _, st := range stats {
				byCap[st.Capability] = st
			}
			for _, t := range pool {
				m := domain.MatchCapabilities(t.RequiredCapabilities, p.Capabilities, p.Tags, settings.CapabilityAliases)
				if !m.Eligible {
					continue
				}
				sc := domain.ScoreTask(scoreInput(t), now, w, ceiling)
				sc.Score = math.Max(0, sc.Score-m.Penalty)
				rel := domain.TaskReliability(t.RequiredCapabilities, func(c string) (int64, int64) { return byCap[c].Completed, byCap[c].Blocked })
				out = append(out, ranked{task: t, score: sc, tier: m.Tier, reliability: rel})
			}
			slices.SortStableFunc(out, func(a, b ranked) int {
				if math.Abs(b.score.Score-a.score.Score) > nearTie {
					return domain.ByScoreDesc(a.score.Score, "", b.score.Score, "")
				}
				if ta, tb := domain.TierRank(a.tier), domain.TierRank(b.tier); ta != tb {
					return ta - tb
				}
				if a.reliability != b.reliability {
					return domain.ByScoreDesc(a.reliability, "", b.reliability, "")
				}
				return domain.ByScoreDesc(0, a.task.OrderKey, 0, b.task.OrderKey)
			})
		case isNotFound(err):
			// no profile: conductor falls back to the capabilities argument
		default:
			return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
		}
	}
	if !profiled {
		for _, t := range pool {
			covered := true
			for _, c := range t.RequiredCapabilities {
				if len(req.Capabilities) > 0 && !slices.Contains(req.Capabilities, c) {
					covered = false
					break
				}
			}
			if covered {
				out = append(out, ranked{task: t, score: domain.ScoreTask(scoreInput(t), now, w, ceiling)})
			}
		}
		slices.SortStableFunc(out, func(a, b ranked) int {
			return domain.ByScoreDesc(a.score.Score, a.task.OrderKey, b.score.Score, b.task.OrderKey)
		})
	}
	resp := RankTasksResp{Ranked: []RankedTask{}}
	for _, r := range out[:min(len(out), limit)] {
		task, err := taskOut(r.task, "")
		if err != nil {
			return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
		}
		sc := r.score
		rt := RankedTask{AgedOut: sc.AgedOut, Reason: sc.Reason, Score: sc.Score, Task: task, Factors: ScoreFactors{
			Aging: factorOut(sc.Aging), Capability: factorOut(sc.Capability), Deadline: factorOut(sc.Deadline),
			Priority: factorOut(sc.Priority), Readiness: factorOut(sc.Readiness)}}
		if profiled {
			rt.Tier, rt.Reliability = CapabilityMatchTier(r.tier), r.reliability
		}
		resp.Ranked = append(resp.Ranked, rt)
	}
	return resp, nil
}

// Orchestrate is orchestrate_subtasks.
func (s rankingSvc) Orchestrate(ctx context.Context, caller domain.Caller, req OrchestrateSubtasksReq) (OrchestrateSubtasksResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "orchestrate_subtasks", domain.GrantSubject{}, true)
	if err != nil {
		return OrchestrateSubtasksResp{}, err
	}
	if err := required("parentTaskId", req.ParentTaskID); err != nil {
		return OrchestrateSubtasksResp{}, err
	}
	if err := required("leaseId", req.LeaseID); err != nil {
		return OrchestrateSubtasksResp{}, err
	}
	if len(req.Children) == 0 || len(req.Children) > maxChildren {
		return OrchestrateSubtasksResp{}, domain.Validation("children is 1 to 50 tasks", map[string]any{"field": "children"})
	}
	for _, c := range req.Children {
		if err := required("children.title", c.Title); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		if err := maxChars("children.title", c.Title, maxTitleChars); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		if err := maxChars("children.description", c.Description, maxDescriptionChars); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		if err := checkPriority(c.Priority); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		if err := checkLabelList("children.requiredCapabilities", c.RequiredCapabilities); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return OrchestrateSubtasksResp{}, err
	}
	// The lease is checked once, inside the transaction and after the idempotency replay: a retry
	// of a call that succeeded presents the lease the fan-out dropped (conductor's note).
	return mutate(ctx, s.c, caller, mutation{
		Tool: "orchestrate_subtasks", Args: map[string]any{"projectId": req.ProjectID, "parentTaskId": req.ParentTaskID},
		ProjectID: req.ProjectID, TaskID: req.ParentTaskID, IdempotencyKey: req.IdempotencyKey, Access: a, LockProject: true,
	}, func(t *txn) (OrchestrateSubtasksResp, error) {
		cur := *t.Task
		now := t.Now.UnixMilli()
		if err := leaseHeld(cur, req.LeaseID, caller, now); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
		if err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		todoCol := statusColumn(board, domain.Todo)
		if todoCol == "" {
			return OrchestrateSubtasksResp{}, domain.Validation("Board has no TODO column", nil)
		}
		// One bounded read for the column's head, then the keys chained in memory toward it, in the
		// children's order (a key per child from one base, never the same key twice).
		// ponytail: the children land at the column's head, as add_subtask's (P-69: no last-key read).
		page, err := t.Tx.ListTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: board.ID, ColumnID: todoCol}, "", 1)
		if err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		upper := ""
		if len(page.Tasks) > 0 {
			upper = page.Tasks[0].OrderKey
		}
		parentID := cur.ID
		resp := OrchestrateSubtasksResp{Children: []Task{}}
		var children []store.Task
		var childIDs []string
		order := ""
		for _, c := range req.Children {
			if order, err = domain.OrderBetween(order, upper); err != nil {
				return OrchestrateSubtasksResp{}, internalf("order key before %q: %v", upper, err)
			}
			priority := c.Priority
			if priority == "" {
				priority = domain.PriorityNormal
			}
			child, err := t.Tx.CreateTask(ctx, store.CreateTaskInput{
				ProjectID: req.ProjectID, BoardID: board.ID, ColumnID: todoCol, Title: c.Title, Description: c.Description,
				Status: domain.Todo, Priority: priority, RequiredCapabilities: c.RequiredCapabilities, ParentTaskID: &parentID,
				OrderKey: order, CreatedBy: caller.Identity, CreatedByAgent: callerAgent(caller), CreatedAt: now,
			})
			if err != nil {
				return OrchestrateSubtasksResp{}, err
			}
			if err := t.event(child, domain.EventTaskCreated, map[string]any{"title": child.Title}); err != nil {
				return OrchestrateSubtasksResp{}, err
			}
			children = append(children, child)
			childIDs = append(childIDs, child.ID)
		}
		for _, child := range children {
			if _, err := t.Tx.AddEdge(ctx, store.AddEdgeInput{ProjectID: req.ProjectID, FromTaskID: child.ID, ToTaskID: parentID,
				Type: store.DependencyBlocks, CreatedBy: caller.Identity}); err != nil {
				return OrchestrateSubtasksResp{}, err
			}
			if err := t.event(cur, domain.EventDependencyAdded, map[string]any{"fromTaskId": child.ID, "type": "blocks"}); err != nil {
				return OrchestrateSubtasksResp{}, err
			}
		}
		ch := store.StatusChange{Status: domain.Blocked, ColumnID: statusColumn(board, domain.Blocked), ClearClaim: true,
			UpdatedBy: caller.Identity, UpdatedAt: now}
		if cur.Assignee == nil || *cur.Assignee == "" {
			me := caller.Identity
			ch.Assignee = &store.Nullable[string]{Value: &me}
		}
		after, err := t.Tx.SetStatus(ctx, req.ProjectID, parentID, cur.Version, ch)
		if err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		t.Wrote(after)
		if err := t.event(after, domain.EventTaskOrchestrated, map[string]any{"childIds": childIDs}); err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		t.RingFor(&cur, after, false)
		for _, child := range children {
			// The edge wrote the child's blocks cache; read it back for the answer, as conductor's
			// children carry blocks [parent].
			got, err := t.Tx.GetTask(ctx, req.ProjectID, child.ID)
			if err != nil {
				return OrchestrateSubtasksResp{}, err
			}
			t.RingFor(nil, got, false)
			out, err := taskOut(got, "")
			if err != nil {
				return OrchestrateSubtasksResp{}, err
			}
			resp.Children = append(resp.Children, out)
		}
		parent, err := withSummary(ctx, t.Tx, after)
		if err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		resp.Parent = parent
		return resp, nil
	})
}
