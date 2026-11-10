package service

import (
	"cmp"
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

// Rank is rank_tasks: the pool (at most five pages; with sprintId its members and the runs of its
// members, as claim_next {sprintId} takes them), and on the profile path the profile and its stats.
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
	var pool, runs []store.Task
	for _, t := range tasks {
		switch {
		case !domain.IsClaimable(claimView(t), now):
		case req.SprintID == "" || slices.Contains(t.SprintIDs, req.SprintID):
			pool = append(pool, t)
		case t.RunOf != nil:
			runs = append(runs, t)
		}
	}
	if len(runs) > 0 {
		in, err := membersInSprint(ctx, s.c.store, req.ProjectID, req.SprintID, tasks, runs)
		if err != nil {
			return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
		}
		for _, r := range runs {
			if in[*r.RunOf] {
				pool = append(pool, r)
			}
		}
	}
	rk := newScorer(settings)
	if req.UseProfile {
		if err := s.c.withProfile(ctx, caller, "rank_tasks", req.ProjectID, &rk); err != nil {
			return RankTasksResp{}, err
		}
	}
	profiled := rk.profile != nil
	out := rk.rank(pool, now, req.Capabilities)
	resp := RankTasksResp{Ranked: []RankedTask{}}
	for _, r := range out[:min(len(out), limit)] {
		task, err := taskOut(r.task, "")
		if err != nil {
			return RankTasksResp{}, s.c.mapErr("rank_tasks", err)
		}
		sc := r.score
		rt := RankedTask{AgedOut: sc.AgedOut, Reason: sc.Reason, Score: sc.Score, Task: task, Factors: factorsOut(sc)}
		if profiled {
			rt.Tier, rt.Reliability = CapabilityMatchTier(r.tier), r.reliability
		}
		resp.Ranked = append(resp.Ranked, rt)
	}
	return resp, nil
}

// membersInSprint is the set of runs' parents that are members of sprintID: a raced member's work is
// in its runs, which the sprint's frontier (claim_next {sprintId}) offers with it. A parent among
// known is judged from it; the rest are read, listByIDsMax at a time.
func membersInSprint(ctx context.Context, st store.Store, projectID, sprintID string, known, runs []store.Task) (map[string]bool, error) {
	byID := make(map[string]store.Task, len(known))
	for _, t := range known {
		byID[t.ID] = t
	}
	var missing []string
	for _, r := range runs {
		if _, ok := byID[*r.RunOf]; !ok && !slices.Contains(missing, *r.RunOf) {
			missing = append(missing, *r.RunOf)
		}
	}
	for chunk := range slices.Chunk(missing, listByIDsMax) {
		got, err := st.ListTasksByIDs(ctx, projectID, chunk)
		if err != nil {
			return nil, err
		}
		for _, t := range got {
			byID[t.ID] = t
		}
	}
	in := map[string]bool{}
	for _, r := range runs {
		if p, ok := byID[*r.RunOf]; ok && slices.Contains(p.SprintIDs, sprintID) {
			in[p.ID] = true
		}
	}
	return in, nil
}

// scorer is the one ranking rank_tasks and next_task {strategy: "score"} share (P-258): the
// project's selectionWeights, agingCeilingMs and capabilityAliases and, on the profile path, the
// caller's registered profile and its skill stats.
type scorer struct {
	weights domain.SelectionWeights
	ceiling time.Duration
	aliases map[string]string
	profile *store.AgentProfile
	byCap   map[string]store.SkillStat
}

func newScorer(settings Settings) scorer {
	w := settings.SelectionWeights
	return scorer{
		weights: domain.SelectionWeights{Priority: w.Priority, Readiness: w.Readiness, Deadline: w.Deadline, Aging: w.Aging, Capability: w.Capability},
		ceiling: time.Duration(settings.AgingCeilingMs) * time.Millisecond,
		aliases: settings.CapabilityAliases,
	}
}

// projectScorer is the project's scorer, without a profile: one statement (GetProject).
func (c *Core) projectScorer(ctx context.Context, projectID, tool string) (scorer, error) {
	proj, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return scorer{}, c.mapErr(tool, err)
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return scorer{}, c.mapErr(tool, err)
	}
	return newScorer(settings), nil
}

// withProfile loads the caller's own profile and its skill stats into sc (FORBIDDEN under another
// agent's label or another identity's id, ownProfileKey); with no profile registered sc is unchanged, and the caller falls
// back to the capabilities argument, as conductor does.
func (c *Core) withProfile(ctx context.Context, caller domain.Caller, tool, projectID string, sc *scorer) error {
	key, err := c.ownProfileKey(ctx, caller, projectID, tool)
	if err != nil {
		return err
	}
	p, err := c.store.GetAgentProfile(ctx, projectID, key)
	switch {
	case isNotFound(err):
		return nil
	case err != nil:
		return c.mapErr(tool, err)
	}
	stats, err := c.store.ListSkillStats(ctx, projectID, key)
	if err != nil {
		return c.mapErr(tool, err)
	}
	sc.profile, sc.byCap = &p, map[string]store.SkillStat{}
	for _, st := range stats {
		sc.byCap[st.Capability] = st
	}
	return nil
}

// covers is the profile path's eligibility: domain.MatchCapabilities over the profile's
// capabilities and tags with the project's aliases. Without a profile it is every task.
func (sc scorer) covers(t store.Task) bool {
	return sc.profile == nil || domain.MatchCapabilities(t.RequiredCapabilities, sc.profile.Capabilities, sc.profile.Tags, sc.aliases).Eligible
}

// rank scores pool, best first. With a profile: the tasks it covers, the tag penalty subtracted,
// near-ties broken by tier, then reliability, then order key. Without one: the tasks whose
// required capabilities are all in caps (every task when caps is empty), by score then order key.
func (sc scorer) rank(pool []store.Task, now time.Time, caps []string) []ranked {
	var out []ranked
	if p := sc.profile; p != nil {
		for _, t := range pool {
			m := domain.MatchCapabilities(t.RequiredCapabilities, p.Capabilities, p.Tags, sc.aliases)
			if !m.Eligible {
				continue
			}
			s := domain.ScoreTask(scoreInput(t), now, sc.weights, sc.ceiling)
			s.Score = math.Max(0, s.Score-m.Penalty)
			rel := domain.TaskReliability(t.RequiredCapabilities, func(c string) (int64, int64) { return sc.byCap[c].Completed, sc.byCap[c].Blocked })
			out = append(out, ranked{task: t, score: s, tier: m.Tier, reliability: rel})
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
		return out
	}
	for _, t := range pool {
		covered := true
		for _, c := range t.RequiredCapabilities {
			if len(caps) > 0 && !slices.Contains(caps, c) {
				covered = false
				break
			}
		}
		if covered {
			out = append(out, ranked{task: t, score: domain.ScoreTask(scoreInput(t), now, sc.weights, sc.ceiling)})
		}
	}
	slices.SortStableFunc(out, func(a, b ranked) int {
		return domain.ByScoreDesc(a.score.Score, a.task.OrderKey, b.score.Score, b.task.OrderKey)
	})
	return out
}

func factorsOut(sc domain.TaskScore) ScoreFactors {
	return ScoreFactors{Aging: factorOut(sc.Aging), Capability: factorOut(sc.Capability), Deadline: factorOut(sc.Deadline),
		Priority: factorOut(sc.Priority), Readiness: factorOut(sc.Readiness)}
}

// Orchestrate is orchestrate_subtasks.
func (s rankingSvc) Orchestrate(ctx context.Context, caller domain.Caller, req OrchestrateSubtasksReq) (OrchestrateSubtasksResp, error) {
	written := make([]domain.Priority, 0, len(req.Children))
	for _, c := range req.Children {
		written = append(written, cmp.Or(c.Priority, domain.PriorityNormal))
	}
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "orchestrate_subtasks", domain.GrantSubject{Priorities: written}, true)
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
		ProjectID: req.ProjectID, TaskID: req.ParentTaskID, IdempotencyKey: req.IdempotencyKey, Request: req, Access: a, LockProject: true,
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
		// One read of the column's last key (store.LastOrderKey), then the keys chained in memory after
		// it, in the children's order: the children land at the column's end, as add_subtask's.
		order, err := t.Tx.LastOrderKey(ctx, req.ProjectID, board.ID, todoCol)
		if err != nil {
			return OrchestrateSubtasksResp{}, err
		}
		parentID := cur.ID
		resp := OrchestrateSubtasksResp{Children: []Task{}}
		var children []store.Task
		var childIDs []string
		for _, c := range req.Children {
			prev := order
			if order, err = domain.OrderBetween(prev, ""); err != nil {
				return OrchestrateSubtasksResp{}, internalf("order key after %q: %v", prev, err)
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
