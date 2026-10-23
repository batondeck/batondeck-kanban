package service

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// AddDependency is add_dependency. The edge and both caches are written by store.AddEdge after
// domain.CheckCycle (CYCLE_DETECTED: mutate rolls the whole call back, so nothing is written);
// status never changes (plan §Ticket states: add_dependency only writes blockedBy). The dependent
// (toTaskId, where the event hangs) is mutate's task and is bumped once; the blocker is bumped once
// when its blocks cache gained the dependent, as conductor's bumpArray does. Serialised per project
// (mutation.LockProject).
func (s contextSvc) AddDependency(ctx context.Context, caller domain.Caller, req AddDependencyReq) (AddDependencyResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_dependency", domain.GrantSubject{}, true)
	if err != nil {
		return AddDependencyResp{}, err
	}
	if err := required("fromTaskId", req.FromTaskID); err != nil {
		return AddDependencyResp{}, err
	}
	if err := required("toTaskId", req.ToTaskID); err != nil {
		return AddDependencyResp{}, err
	}
	typ := req.Type
	if typ == "" {
		typ = "blocks"
	}
	if !slices.Contains(DependencyTypeValues, typ) {
		return AddDependencyResp{}, domain.Validation("unknown dependency type: "+string(typ), map[string]any{"field": "type"})
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddDependencyResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_dependency", Args: map[string]any{"projectId": req.ProjectID, "fromTaskId": req.FromTaskID, "toTaskId": req.ToTaskID},
		ProjectID: req.ProjectID, TaskID: req.ToTaskID, IdempotencyKey: req.IdempotencyKey, Access: a, LockProject: true,
	}, func(t *txn) (AddDependencyResp, error) {
		gains := typ == "blocks" && req.FromTaskID != req.ToTaskID && !slices.Contains(t.Task.BlockedBy, req.FromTaskID)
		edge, err := t.Tx.AddEdge(ctx, store.AddEdgeInput{ProjectID: req.ProjectID, FromTaskID: req.FromTaskID, ToTaskID: req.ToTaskID, Type: store.DependencyType(typ), CreatedBy: caller.Identity})
		if err != nil {
			return AddDependencyResp{}, err
		}
		if gains {
			if err := t.bumpOther(req.FromTaskID); err != nil {
				return AddDependencyResp{}, err
			}
		}
		data, err := json.Marshal(map[string]any{"fromTaskId": req.FromTaskID, "type": typ})
		if err != nil {
			return AddDependencyResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventDependencyAdded, Data: data})
		return AddDependencyResp{Edge: edgeOut(edge)}, nil
	})
}

// RemoveDependency is remove_dependency. store.RemoveEdge drops the edge and, for blocks, the
// blocker from the dependent's blockedBy (and the dependent from the blocker's blocks); each of the
// two tasks is then bumped once. The edge's removal clears its blocker, so the dependent goes
// through domain.DecideUnblock: when that is AutoReady (blockedBy now empty, BLOCKED, not poison) it
// is moved to READY by unblockDependent, with its task.dependency_cleared event and doorbell; a
// poisoned dependent stays BLOCKED. Event dependency.removed {edgeId} on the dependent. Serialised
// per project (mutation.LockProject). The dependent is unknown until the edge is read, so mutate
// loads no task and its pending grant check is finished here, against the dependent.
func (s contextSvc) RemoveDependency(ctx context.Context, caller domain.Caller, req RemoveDependencyReq) (RemoveDependencyResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "remove_dependency", domain.GrantSubject{}, true)
	if err != nil {
		return RemoveDependencyResp{}, err
	}
	if err := required("edgeId", req.EdgeID); err != nil {
		return RemoveDependencyResp{}, err
	}
	pend := a
	a.pending = false
	return mutate(ctx, s.c, caller, mutation{
		Tool: "remove_dependency", Args: map[string]any{"projectId": req.ProjectID, "edgeId": req.EdgeID},
		ProjectID: req.ProjectID, Access: a, LockProject: true,
	}, func(t *txn) (RemoveDependencyResp, error) {
		edge, err := t.Tx.RemoveEdge(ctx, req.ProjectID, req.EdgeID)
		if err != nil {
			return RemoveDependencyResp{}, err
		}
		dep, err := t.Tx.GetTask(ctx, req.ProjectID, edge.ToTaskID)
		if err != nil {
			return RemoveDependencyResp{}, err
		}
		if err := pend.permit(&dep); err != nil {
			return RemoveDependencyResp{}, err
		}
		data, err := json.Marshal(map[string]any{"edgeId": edge.ID})
		if err != nil {
			return RemoveDependencyResp{}, err
		}
		t.Event(store.EventInput{TaskID: dep.ID, BoardID: dep.BoardID, Type: domain.EventDependencyRemoved, Data: data})
		if edge.Type != store.DependencyBlocks {
			t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: dep.ID, BoardID: dep.BoardID})
			return RemoveDependencyResp{Ok: true}, nil
		}
		if edge.FromTaskID != edge.ToTaskID {
			if err := t.bumpOther(edge.FromTaskID); err != nil {
				return RemoveDependencyResp{}, err
			}
		}
		// RemoveEdge has already dropped the blocker from the cache: the decision is over the
		// dependent as it was, with the blocker restored.
		before := dep
		if !slices.Contains(before.BlockedBy, edge.FromTaskID) {
			before.BlockedBy = append(slices.Clone(dep.BlockedBy), edge.FromTaskID)
		}
		if d := domain.DecideUnblock(before.BlockedBy, dep.Status, reasonOf(dep), edge.FromTaskID); d.AutoReady {
			if _, err := t.unblockDependent(dep, edge.FromTaskID, d); err != nil {
				return RemoveDependencyResp{}, err
			}
			return RemoveDependencyResp{Ok: true}, nil
		}
		after, err := t.bump(dep)
		if err != nil {
			return RemoveDependencyResp{}, err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: after.ID, BoardID: after.BoardID})
		return RemoveDependencyResp{Ok: true}, nil
	})
}

func reasonOf(t store.Task) string {
	if t.BlockedReason == nil {
		return ""
	}
	return *t.BlockedReason
}

// bump moves task's version by one with no other change (the caller wrote something versionless
// on it: a dependency cache).
func (t *txn) bump(task store.Task) (store.Task, error) {
	return t.Tx.UpdateTask(t.ctx, task.ProjectID, task.ID, task.Version, store.TaskPatch{UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli()})
}

// bumpOther bumps a task the mutation wrote a cache of besides its own: read, then bumped once.
func (t *txn) bumpOther(taskID string) error {
	other, err := t.Tx.GetTask(t.ctx, t.ProjectID, taskID)
	if err != nil {
		return err
	}
	_, err = t.bump(other)
	return err
}

// unblockDependent is the auto-unblock entry point (plan §Fold 1), the one place a dependent moves
// because a blocker cleared: remove_dependency here; complete_task and move_task to DONE (R1.5,
// R1.3) and the delete cascade (R1.3) call it with each dependent whose blockedBy names the cleared
// blocker. dep is the dependent as read inside the transaction (never mutate's loaded task) and d
// is domain.DecideUnblock over its blockedBy with clearedID; d.Cleared must be true. It writes d's
// blockedBy when dep's cache still names clearedID (Dependencies.SetCaches), then on d.AutoReady
// moves dep to READY in its board's READY column (the column it is in when the board has none),
// else bumps it; appends task.dependency_cleared {clearedBy, autoReady} on dep; and records the
// task and its doorbell (RingFor, unblocked: an assignee is rung assigned, no assignee broadcast).
// It returns dep after. A caller holding the project lock (mutation.LockProject) took it first.
func (t *txn) unblockDependent(dep store.Task, clearedID string, d domain.UnblockDecision) (store.Task, error) {
	if !d.Cleared {
		return store.Task{}, internalf("unblockDependent: %s does not clear %s", clearedID, dep.ID)
	}
	if t.Task != nil && t.Task.ID == dep.ID {
		return store.Task{}, internalf("unblockDependent: %s is the mutation's own task", dep.ID)
	}
	if slices.Contains(dep.BlockedBy, clearedID) {
		if err := t.Tx.SetCaches(t.ctx, dep.ProjectID, dep.ID, d.BlockedBy, dep.Blocks); err != nil {
			return store.Task{}, err
		}
	}
	var after store.Task
	var err error
	if d.AutoReady {
		board, _, err := t.Tx.GetBoard(t.ctx, dep.ProjectID, dep.BoardID)
		if err != nil {
			return store.Task{}, err
		}
		after, err = t.Tx.SetStatus(t.ctx, dep.ProjectID, dep.ID, dep.Version, store.StatusChange{
			Status: domain.Ready, ColumnID: readyColumn(board), UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli(),
		})
		if err != nil {
			return store.Task{}, err
		}
	} else if after, err = t.bump(dep); err != nil {
		return store.Task{}, err
	}
	data, err := json.Marshal(map[string]any{"clearedBy": clearedID, "autoReady": d.AutoReady})
	if err != nil {
		return store.Task{}, err
	}
	t.Event(store.EventInput{TaskID: dep.ID, BoardID: dep.BoardID, Type: domain.EventTaskDependencyCleared, Data: data})
	bells := doorbellsFor(&dep, after, true)
	if len(bells) == 0 {
		bells = []Doorbell{{Kind: ChangeKindChanged, TaskID: after.ID, BoardID: after.BoardID}}
	}
	for _, b := range bells {
		t.Ring(b)
	}
	return after, nil
}

// readyColumn is the board's first READY column, or "" (the task keeps its column), conductor's
// columnIdForStatus.
func readyColumn(b store.Board) string {
	for _, c := range b.Columns {
		if c.Status == domain.Ready {
			return c.ID
		}
	}
	return ""
}
