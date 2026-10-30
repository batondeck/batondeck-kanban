package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Runs is conductor's runs.ts on the five states: a race is the racing flag on a parent and N run
// tasks (runOf = the parent), and a losing run leaves by the delete cascade (deleteTree), never by a
// status (plan §Fold 2). start_runs, pick_run and cancel_runs move the parent once (one SetStatus
// carrying the race flag); open_run does not touch it.
func (c *Core) Runs() Runs { return runsSvc{c} }

type runsSvc struct{ c *Core }

// defaultMaxRunsPerTask is conductor's DEFAULT_MAX_RUNS_PER_TASK (types.ts:602), the cap when the
// project sets no maxRunsPerTask.
const defaultMaxRunsPerTask = 5

// maxRunCount is the largest count start_runs takes (conductor's z.number().max(50)).
const maxRunCount = 50

// runSettings is the project settings a race reads.
func (s runsSvc) runSettings(ctx context.Context, projectID string) (store.ProjectSettings, Settings, error) {
	proj, err := s.c.store.GetProject(ctx, projectID)
	if err != nil {
		return store.ProjectSettings{}, Settings{}, err
	}
	out, err := settingsOut(proj.Settings)
	if err != nil {
		return store.ProjectSettings{}, Settings{}, err
	}
	if out.MaxRunsPerTask <= 0 {
		out.MaxRunsPerTask = defaultMaxRunsPerTask
	}
	return proj.Settings, out, nil
}

// raceKey binds a caller's idempotency key to the parent, conductor's Fix 5: without it one key
// reused on two parents would replay the first parent's runs for the second.
func raceKey(key, parentID string) string {
	if key == "" {
		return ""
	}
	return key + ":" + parentID
}

// isSubmitted is conductor's runs.ts:72: a run is pickable once it is REVIEW or DONE with a
// deliverable. A cancelled run is DONE but was not delivered (ADR-0001), so it is never submitted
// (P-208).
func isSubmitted(r store.Task) bool {
	return (r.Status == domain.Review || r.Status == domain.Done) && r.Deliverable != nil && *r.Deliverable != "" &&
		!slices.Contains(r.Labels, domain.LabelCancelled)
}

// runCopyLabels is the parent's labels a run carries: the system labels stay with the parent
// (ADR-0001: only the server writes them, on the task they describe).
func runCopyLabels(labels []string) []string {
	out := slices.Clone(labels)
	for _, l := range domain.SystemLabels() {
		out = domain.WithoutLabel(out, l)
	}
	return out
}

// createRun creates run ordinal k of parent in the board's TODO column at order key order: a real
// task copying the parent's brief, priority, labels, capabilities and custom fields, its runs row, and
// task.created and run.opened on it. A named assignee is rung assigned, an unnamed run broadcast.
func (t *txn) createRun(parent store.Task, board store.Board, k int64, brief string, assignee *string, order string) (store.Task, error) {
	col := todoColumn(board)
	if col == "" {
		return store.Task{}, domain.Validation("Board has no TODO column", map[string]any{"boardId": board.ID})
	}
	desc := parent.Description
	if brief != "" {
		desc = brief
	}
	now := t.Now.UnixMilli()
	r, err := t.Tx.CreateTask(t.ctx, store.CreateTaskInput{
		ProjectID: parent.ProjectID, BoardID: parent.BoardID, ColumnID: col, Title: parent.Title + " · run " + strconv.FormatInt(k, 10),
		Description: desc, Status: domain.Todo, Priority: parent.Priority, Assignee: assignee, Labels: runCopyLabels(parent.Labels),
		RequiredCapabilities: slices.Clone(parent.RequiredCapabilities), OrderKey: order, CustomFields: parent.CustomFields,
		RunOf: &parent.ID, CreatedBy: t.Caller.Identity, CreatedByAgent: callerAgent(t.Caller), CreatedAt: now,
	})
	if err != nil {
		return store.Task{}, err
	}
	if err := t.Tx.InsertRun(t.ctx, store.Run{ProjectID: r.ProjectID, RunTaskID: r.ID, ParentTaskID: parent.ID, Ordinal: k, OpenedBy: t.Caller.Identity, OpenedAt: now}); err != nil {
		return store.Task{}, err
	}
	for _, e := range []struct {
		typ  domain.EventType
		data map[string]any
	}{{domain.EventTaskCreated, map[string]any{"title": r.Title}}, {domain.EventRunOpened, map[string]any{"runOf": parent.ID, "index": k}}} {
		data, err := json.Marshal(e.data)
		if err != nil {
			return store.Task{}, err
		}
		t.Event(store.EventInput{TaskID: r.ID, BoardID: r.BoardID, Type: e.typ, Data: data})
	}
	t.RingFor(nil, r, false)
	return r, nil
}

// nextOrder is a key after after ("" is the first key).
func nextOrder(after string) (string, error) {
	key, err := domain.OrderBetween(after, "")
	if err != nil {
		return "", internalf("order key after %q: %v", after, err)
	}
	return key, nil
}

func runsOut(runs []store.Task) ([]Task, error) {
	out := make([]Task, 0, len(runs))
	for _, r := range runs {
		v, err := taskOut(r, "")
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Start is start_runs (conductor runs.ts:205-323): the parent, version-checked, goes racing in one
// write (an IN_PROGRESS parent drops to TODO, any lease is dropped) and n runs are created in the
// same transaction. n is len(agents) when agents is sent, else count, else 1. Every status may
// enter a race on the fixed table (each reaches IN_PROGRESS), but a poisoned parent may not: its
// conductor counterpart, DEAD_LETTER, had no edge to IN_PROGRESS. A retried call with the same
// idempotencyKey replays the race it opened (the mutation's replay: no per-run keys).
func (s runsSvc) Start(ctx context.Context, caller domain.Caller, req StartRunsReq) (StartRunsResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "start_runs", domain.GrantSubject{}, true)
	if err != nil {
		return StartRunsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return StartRunsResp{}, err
	}
	if req.Version < 0 {
		return StartRunsResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	if req.Count < 0 || req.Count > maxRunCount {
		return StartRunsResp{}, domain.Validation(fmt.Sprintf("count must be 1 to %d", maxRunCount), map[string]any{"field": "count"})
	}
	if err := checkLabelList("agents", req.Agents); err != nil {
		return StartRunsResp{}, err
	}
	if err := maxChars("brief", req.Brief, maxSummaryChars); err != nil {
		return StartRunsResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return StartRunsResp{}, err
	}
	n := int64(1)
	switch {
	case req.Agents != nil:
		n = int64(len(req.Agents))
	case req.Count > 0:
		n = req.Count
	}
	if n < 1 {
		return StartRunsResp{}, domain.Validation("start_runs needs at least one run (count or agents)", nil)
	}
	_, settings, err := s.runSettings(ctx, req.ProjectID)
	if err != nil {
		return StartRunsResp{}, s.c.mapErr("start_runs", err)
	}
	if n > settings.MaxRunsPerTask {
		return StartRunsResp{}, domain.Validation(fmt.Sprintf("Too many runs: %d requested, max %d per task", n, settings.MaxRunsPerTask),
			map[string]any{"requested": n, "max": settings.MaxRunsPerTask})
	}
	version := req.Version
	return mutate(ctx, s.c, caller, mutation{
		Tool: "start_runs", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, IdempotencyKey: raceKey(req.IdempotencyKey, req.TaskID), Access: a,
	}, func(t *txn) (StartRunsResp, error) {
		cur := *t.Task
		if cur.Racing {
			return StartRunsResp{}, domain.InvalidTransition("RACING", "RACING", nil, nil)
		}
		if domain.IsPoisoned(cur.Status, reasonOf(cur)) {
			return StartRunsResp{}, domain.InvalidTransition(cur.Status, domain.InProgress, domain.AllowedNext(cur.Status), nil)
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
		if err != nil {
			return StartRunsResp{}, err
		}
		on, now := true, t.Now.UnixMilli()
		ch := store.StatusChange{Status: cur.Status, BlockedReason: reasonOf(cur), ClearClaim: cur.Claim != nil, Racing: &on,
			RaceOpenedAt: &store.Nullable[int64]{Value: &now}, UpdatedBy: caller.Identity, UpdatedAt: now}
		if cur.Status == domain.InProgress {
			// A racing parent never holds an IN_PROGRESS WIP slot (conductor's Fix 2).
			ch.Status, ch.ColumnID = domain.Todo, todoColumn(board)
		}
		after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, ch)
		if err != nil {
			return StartRunsResp{}, err
		}
		t.Wrote(after)
		if err := t.event(after, domain.EventRunsStarted, map[string]any{"count": n}); err != nil {
			return StartRunsResp{}, err
		}
		t.ringChanged(after)
		rows, err := t.Tx.ListRuns(ctx, req.ProjectID, cur.ID)
		if err != nil {
			return StartRunsResp{}, err
		}
		k := int64(0)
		for _, r := range rows {
			k = max(k, r.Ordinal)
		}
		order := cur.OrderKey
		var runs []store.Task
		for i := range n {
			if order, err = nextOrder(order); err != nil {
				return StartRunsResp{}, err
			}
			var assignee *string
			if req.Agents != nil {
				name := req.Agents[i]
				assignee = &name
			}
			k++
			r, err := t.createRun(after, board, k, req.Brief, assignee, order)
			if err != nil {
				return StartRunsResp{}, err
			}
			runs = append(runs, r)
		}
		task, err := withSummary(ctx, t.Tx, after)
		if err != nil {
			return StartRunsResp{}, err
		}
		out, err := runsOut(runs)
		if err != nil {
			return StartRunsResp{}, err
		}
		return StartRunsResp{Task: task, Runs: out}, nil
	})
}

// Open is open_run (conductor runs.ts:325-383): one more run of a racing parent, ordinal one past
// the highest recorded, ordered after the last run. The parent is read, not written: its version
// does not move. Capped by maxRunsPerTask counted over the runs that exist.
func (s runsSvc) Open(ctx context.Context, caller domain.Caller, req OpenRunReq) (OpenRunResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "open_run", domain.GrantSubject{}, true)
	if err != nil {
		return OpenRunResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return OpenRunResp{}, err
	}
	if req.Agent != "" {
		if err := checkLabel("agent", req.Agent); err != nil {
			return OpenRunResp{}, err
		}
	}
	if err := maxChars("brief", req.Brief, maxSummaryChars); err != nil {
		return OpenRunResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return OpenRunResp{}, err
	}
	_, settings, err := s.runSettings(ctx, req.ProjectID)
	if err != nil {
		return OpenRunResp{}, s.c.mapErr("open_run", err)
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "open_run", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, IdempotencyKey: raceKey(req.IdempotencyKey, req.TaskID),
	}, func(t *txn) (OpenRunResp, error) {
		parent, err := t.Tx.GetTask(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return OpenRunResp{}, err
		}
		if err := a.permit(&parent); err != nil {
			return OpenRunResp{}, err
		}
		if !parent.Racing {
			return OpenRunResp{}, domain.InvalidTransition(parent.Status, "racing", nil, nil)
		}
		existing, err := t.runsOf(parent)
		if err != nil {
			return OpenRunResp{}, err
		}
		if live := int64(len(existing)); live >= settings.MaxRunsPerTask {
			return OpenRunResp{}, domain.Validation(fmt.Sprintf("Run cap reached: %d/%d open", live, settings.MaxRunsPerTask),
				map[string]any{"live": live, "max": settings.MaxRunsPerTask})
		}
		rows, err := t.Tx.ListRuns(ctx, req.ProjectID, parent.ID)
		if err != nil {
			return OpenRunResp{}, err
		}
		k := int64(0)
		for _, r := range rows {
			k = max(k, r.Ordinal)
		}
		last := parent.OrderKey
		for _, r := range existing {
			last = max(last, r.OrderKey)
		}
		order, err := nextOrder(last)
		if err != nil {
			return OpenRunResp{}, err
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, parent.BoardID)
		if err != nil {
			return OpenRunResp{}, err
		}
		var assignee *string
		if req.Agent != "" {
			assignee = &req.Agent
		}
		r, err := t.createRun(parent, board, k+1, req.Brief, assignee, order)
		if err != nil {
			return OpenRunResp{}, err
		}
		out, err := taskOut(r, "")
		if err != nil {
			return OpenRunResp{}, err
		}
		return OpenRunResp{Run: out}, nil
	})
}

// Pick is pick_run (conductor runs.ts:403-519), in one transaction under the project lock: every
// run but the winner is deleted through the cascade first (each run.discarded on the parent while it
// still races, task.deleted on the board), then the parent takes the winner's deliverable and lands
// REVIEW (reviewEnabled, with the handover record) or DONE with racing false, in one write
// (run.picked); DONE unblocks its dependents. A DONE loser is deleted too: conductor kept it only
// because CANCELLED would have overwritten it. The winner keeps runOf. The winner's live artifacts
// are promoted onto the parent (promoteArtifacts), so a picked parent is not a second completion path
// that lands REVIEW or DONE carrying no evidence (conductor T-52).
func (s runsSvc) Pick(ctx context.Context, caller domain.Caller, req PickRunReq) (PickRunResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "pick_run", domain.GrantSubject{}, true)
	if err != nil {
		return PickRunResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return PickRunResp{}, err
	}
	if err := required("runTaskId", req.RunTaskID); err != nil {
		return PickRunResp{}, err
	}
	if req.Version < 0 {
		return PickRunResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	ps, settings, err := s.runSettings(ctx, req.ProjectID)
	if err != nil {
		return PickRunResp{}, s.c.mapErr("pick_run", err)
	}
	target := domain.Done
	if ps.ReviewEnabled {
		target = domain.Review
	}
	version := req.Version
	var blobs []string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "pick_run", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a, LockProject: true,
	}, func(t *txn) (PickRunResp, error) {
		cur := *t.Task
		if !cur.Racing {
			return PickRunResp{}, domain.InvalidTransition(cur.Status, "pick_run", nil, nil)
		}
		winner, err := t.Tx.GetTask(ctx, req.ProjectID, req.RunTaskID)
		if err != nil {
			return PickRunResp{}, err
		}
		if winner.RunOf == nil || *winner.RunOf != cur.ID {
			return PickRunResp{}, domain.Validation("runTaskId is not a run of this task", map[string]any{"runTaskId": req.RunTaskID, "taskId": req.TaskID})
		}
		if !isSubmitted(winner) {
			return PickRunResp{}, domain.InvalidTransition(winner.Status, "picked", nil, nil)
		}
		runs, err := t.runsOf(cur)
		if err != nil {
			return PickRunResp{}, err
		}
		blobs = nil
		for _, r := range runs {
			if r.ID == winner.ID {
				continue
			}
			freed, err := t.deleteTree(r)
			if err != nil {
				return PickRunResp{}, err
			}
			blobs = append(blobs, freed...)
		}
		promoted, err := t.promoteArtifacts(cur, winner)
		if err != nil {
			return PickRunResp{}, err
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
		if err != nil {
			return PickRunResp{}, err
		}
		off, now := false, t.Now.UnixMilli()
		ch := store.StatusChange{Status: target, ColumnID: statusColumn(board, target), ClearClaim: true, Racing: &off,
			RaceOpenedAt: &store.Nullable[int64]{}, Deliverable: &store.Nullable[string]{Value: winner.Deliverable},
			UpdatedBy: caller.Identity, UpdatedAt: now}
		if target == domain.Review {
			raw, err := t.pickReview(cur, winner, settings, now)
			if err != nil {
				return PickRunResp{}, err
			}
			ch.Review = &store.Nullable[json.RawMessage]{Value: &raw}
		}
		after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, ch)
		if err != nil {
			return PickRunResp{}, err
		}
		t.Wrote(after)
		if err := t.event(after, domain.EventRunPicked, map[string]any{"runTaskId": winner.ID, "to": target, "artifacts": promoted}); err != nil {
			return PickRunResp{}, err
		}
		t.ringChanged(after)
		if target == domain.Done {
			if err := t.unblockDependentsOf(cur.ID, nil); err != nil {
				return PickRunResp{}, err
			}
		}
		task, err := t.taskWithContext(after)
		if err != nil {
			return PickRunResp{}, err
		}
		return PickRunResp{Task: task}, nil
	})
	if err != nil {
		return PickRunResp{}, err
	}
	s.c.blobGC(ctx, blobs)
	return out, nil
}

// promoteArtifacts is conductor's mergeArtifacts(parent.artifacts, winner.artifacts) (runs.ts:473):
// each live artifact of the winner whose key (artifactKey) the parent's live artifacts do not hold is
// appended to the parent, its author kept, until the parent holds maxArtifactsPerTask, the cap
// add_artifact counts tombstones against. It answers how many it appended. The rows are the
// artifact area's own; the parent's one versioned write is pick_run's.
// ponytail: past the cap the rest are left on the winner, which keeps them; raise it with the cap.
func (t *txn) promoteArtifacts(parent, winner store.Task) (int, error) {
	from, err := t.Tx.ListArtifacts(t.ctx, winner.ProjectID, winner.ID)
	if err != nil {
		return 0, err
	}
	have, err := t.Tx.ListArtifacts(t.ctx, parent.ProjectID, parent.ID)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, a := range have {
		if a.Retraction == nil {
			seen[artifactKey(ArtifactKind(a.Kind), a.URL, a.Ref)] = true
		}
	}
	n := 0
	for _, a := range from {
		k := artifactKey(ArtifactKind(a.Kind), a.URL, a.Ref)
		if a.Retraction != nil || seen[k] || len(have)+n >= maxArtifactsPerTask {
			continue
		}
		seen[k] = true
		if _, err := t.Tx.AddArtifact(t.ctx, store.AddArtifactInput{ProjectID: parent.ProjectID, TaskID: parent.ID, Kind: a.Kind, URL: a.URL,
			Ref: a.Ref, Repo: a.Repo, Title: a.Title, AddedBy: a.AddedBy, AddedByAgent: a.AddedByAgent}); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

// pickReview is the handover record a picked parent lands REVIEW with (conductor T-109): completed
// by the winner's completer (its review's, else its lease holder, else the picker), requested by the
// picker; the reviewer is the parent's or the project default, recorded when it is a member. No
// assignee change and no task.review_requested: conductor's ponytail.
func (t *txn) pickReview(parent, winner store.Task, settings Settings, now int64) (json.RawMessage, error) {
	review := TaskReview{CompletedBy: t.Caller.Identity, RequestedBy: t.Caller.Identity, RequestedAt: isoMs(now)}
	if winner.Claim != nil {
		review.CompletedBy, review.CompletedByAgent = winner.Claim.Owner, winner.Claim.Agent
	}
	if prior, err := reviewOut(winner.Review); err != nil {
		return nil, err
	} else if prior != nil && prior.CompletedBy != "" {
		review.CompletedBy, review.CompletedByAgent = prior.CompletedBy, prior.CompletedByAgent
	}
	reviewer := ""
	if parent.Reviewer != nil {
		reviewer = *parent.Reviewer
	} else if settings.DefaultReviewer != nil {
		reviewer = *settings.DefaultReviewer
	}
	if reviewer != "" {
		member, err := t.isMember(reviewer)
		if err != nil {
			return nil, err
		}
		if member {
			review.Reviewer = &reviewer
		}
	}
	return json.Marshal(review)
}

// Cancel is cancel_runs (conductor runs.ts:521-567): every run is deleted through the cascade, and
// the last one's settleRace ends the race on the parent (TODO, racing false, one write); a racing
// parent with no run left is ended directly. Event runs.cancelled on the parent. A DONE run is
// deleted too (no CANCELLED to keep it under).
func (s runsSvc) Cancel(ctx context.Context, caller domain.Caller, req CancelRunsReq) (CancelRunsResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "cancel_runs", domain.GrantSubject{}, true)
	if err != nil {
		return CancelRunsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return CancelRunsResp{}, err
	}
	if req.Version < 0 {
		return CancelRunsResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	version := req.Version
	var blobs []string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "cancel_runs", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a, LockProject: true,
	}, func(t *txn) (CancelRunsResp, error) {
		cur := *t.Task
		if !cur.Racing {
			return CancelRunsResp{}, domain.InvalidTransition(cur.Status, "cancel_runs", nil, nil)
		}
		runs, err := t.runsOf(cur)
		if err != nil {
			return CancelRunsResp{}, err
		}
		blobs = nil
		for _, r := range runs {
			freed, err := t.deleteTree(r)
			if err != nil {
				return CancelRunsResp{}, err
			}
			blobs = append(blobs, freed...)
		}
		if len(runs) == 0 {
			if err := t.endRace(cur); err != nil {
				return CancelRunsResp{}, err
			}
		}
		after, err := t.Tx.GetTask(ctx, req.ProjectID, cur.ID)
		if err != nil {
			return CancelRunsResp{}, err
		}
		if err := t.event(after, domain.EventRunsCancelled, map[string]any{}); err != nil {
			return CancelRunsResp{}, err
		}
		task, err := withSummary(ctx, t.Tx, after)
		if err != nil {
			return CancelRunsResp{}, err
		}
		return CancelRunsResp{Task: task}, nil
	})
	if err != nil {
		return CancelRunsResp{}, err
	}
	s.c.blobGC(ctx, blobs)
	return out, nil
}

// List is list_runs (conductor runs.ts:385-401): every run of the parent that exists, in order, as
// a RunView; the lease holder is the live lease's agent, else its owner. There is no CANCELLED
// filter: a losing run is gone. An unknown parent lists none, as conductor's listRuns does. Two
// statements: the parent (for its board) and one page of its runs.
func (s runsSvc) List(ctx context.Context, caller domain.Caller, req ListRunsReq) (ListRunsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_runs", domain.GrantSubject{}, true); err != nil {
		return ListRunsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ListRunsResp{}, err
	}
	out := ListRunsResp{Runs: []RunView{}}
	parent, err := s.c.store.GetTask(ctx, req.ProjectID, req.TaskID)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return out, nil
	}
	if err != nil {
		return ListRunsResp{}, s.c.mapErr("list_runs", err)
	}
	page, err := s.c.store.ListTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: parent.BoardID, RunOf: &parent.ID}, "", listMaxLimit)
	if err != nil {
		return ListRunsResp{}, s.c.mapErr("list_runs", err)
	}
	now := s.c.nowMs()
	for _, r := range page.Tasks {
		v := RunView{Assignee: r.Assignee, HasDeliverable: r.Deliverable != nil && *r.Deliverable != "", Status: r.Status, TaskID: r.ID,
			Version: float64(r.Version), Deliverable: r.Deliverable}
		if liveLease(r, now) {
			holder := r.Claim.Owner
			if r.Claim.Agent != nil {
				holder = *r.Claim.Agent
			}
			v.LeaseHolder = &holder
		}
		out.Runs = append(out.Runs, v)
	}
	return out, nil
}
