package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// BlobGC is the delete cascade's blob hook: called once after each delete_task commits, with the
// content hashes of the deleted attachments' blobs (none while attachments have no blobs, R1.8).
// R1.8 fills it; the Options default is a no-op.
type BlobGC func(ctx context.Context, sha256s []string)

// Delete is delete_task (plan §Fold 2), master tier: version-checked by mutate (STALE), another
// identity's live lease CONFLICT_LOCKED, a missing task (a second call) NOT_FOUND. One mutation runs
// deleteCascade under the project lock; the board-level task.deleted {id, title, status}, the audit
// row (domain.DescribeAudit) and a change-log row per deleted task are written by mutate. After the
// commit the blob hook is called with the freed hashes. The T-n is never reused: the store's counter
// does not go down.
func (s tasksSvc) Delete(ctx context.Context, caller domain.Caller, req DeleteTaskReq) (DeleteTaskResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "delete_task", domain.GrantSubject{}, true)
	if err != nil {
		return DeleteTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return DeleteTaskResp{}, err
	}
	if req.Version < 0 {
		return DeleteTaskResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	version := req.Version
	var blobs []string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "delete_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID, "version": req.Version},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a, LockProject: true,
	}, func(t *txn) (DeleteTaskResp, error) {
		root := *t.Task
		if root.Claim != nil && root.Claim.Owner != caller.Identity && domain.HasLiveLease(&domain.Lease{ExpiresAt: time.UnixMilli(root.Claim.ExpiresAt)}, t.Now) {
			return DeleteTaskResp{}, domain.ConflictLocked(root.ID, root.Claim.Owner)
		}
		var err error
		if blobs, err = t.deleteCascade(root); err != nil {
			return DeleteTaskResp{}, err
		}
		return DeleteTaskResp{Ok: true}, nil
	})
	if err != nil {
		return DeleteTaskResp{}, err
	}
	s.c.blobGC(ctx, blobs)
	return out, nil
}

// deleteCascade is plan §Fold 2's cascade sentence over root, mutate's loaded task, in order:
//  1. the deletion set: root, then its parentTaskId children and runOf runs, recursively;
//  2. each dependent outside the set: the deleted id stripped from its blockedBy and the
//     poison-aware unblock (unblockDependent, R1.17's entry) — BEFORE DeleteTask, which drops the
//     edges but not the other tasks' caches (R0.10 review);
//  3. each blocker outside the set: the deleted id stripped from its blocks, the blocker bumped;
//  4. root's parent: root stripped from its blockedBy and the same unblock;
//  5. each member's task-scope memory and attachments, then the task (its events, context items,
//     summary, edges, comments and follow-ups go with it: the schema cascades);
//  6. a run's parent: if no run is left and it is racing, racing:false and TODO (cancel_runs);
//  7. the board-level task.deleted {id, title, status} on root, a change-log row per deleted task.
//
// It returns the blob hashes the deleted attachments held. Idempotency keys are kept: they are keyed
// (project, identity, key), not by task, so a create replayed within the TTL answers the deleted task.
func (t *txn) deleteCascade(root store.Task) ([]string, error) {
	set, err := t.deletionSet(root)
	if err != nil {
		return nil, err
	}
	in := make(map[string]bool, len(set))
	for _, x := range set {
		in[x.ID] = true
	}
	for _, x := range set {
		if err := t.unblockDependentsOf(x.ID, in); err != nil {
			return nil, err
		}
		if err := t.stripFromBlockers(x.ID, in); err != nil {
			return nil, err
		}
	}
	if root.ParentTaskID != nil && !in[*root.ParentTaskID] {
		if err := t.unblockOne(*root.ParentTaskID, root.ID); err != nil {
			return nil, err
		}
	}
	blobs := []string{}
	for _, x := range slices.Backward(set) {
		if _, err := t.Tx.DeleteMemoryByTask(t.ctx, x.ProjectID, x.ID); err != nil {
			return nil, err
		}
		freed, err := t.deleteAttachments(x)
		if err != nil {
			return nil, err
		}
		blobs = append(blobs, freed...)
		if err := t.Tx.DeleteTask(t.ctx, x.ProjectID, x.ID, x.Version); err != nil {
			return nil, err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: x.ID, BoardID: x.BoardID})
	}
	if root.RunOf != nil && !in[*root.RunOf] {
		if err := t.settleRace(*root.RunOf, root.ID); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(map[string]any{"id": root.ID, "title": root.Title, "status": root.Status})
	if err != nil {
		return nil, err
	}
	t.BoardEvent(store.EventInput{BoardID: root.BoardID, Type: domain.EventTaskDeleted, Data: data})
	t.Deleted()
	return blobs, nil
}

// deletionSet is root and, breadth first, every task whose parentTaskId or runOf is a member.
func (t *txn) deletionSet(root store.Task) ([]store.Task, error) {
	set := []store.Task{root}
	seen := map[string]bool{root.ID: true}
	for i := 0; i < len(set); i++ {
		x := set[i]
		ids, err := t.Tx.ListSubtasks(t.ctx, x.ProjectID, x.ID)
		if err != nil {
			return nil, err
		}
		runs, err := t.runsOf(x)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if !slices.Contains(ids, r.ID) {
				ids = append(ids, r.ID)
			}
		}
		var fresh []string
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				fresh = append(fresh, id)
			}
		}
		for chunk := range slices.Chunk(fresh, listByIDsMax) {
			tasks, err := t.Tx.ListTasksByIDs(t.ctx, x.ProjectID, chunk)
			if err != nil {
				return nil, err
			}
			set = append(set, tasks...)
		}
	}
	return set, nil
}

// runsOf is every run of x (the tasks of x's board whose runOf is x), paged to the end.
func (t *txn) runsOf(x store.Task) ([]store.Task, error) {
	var out []store.Task
	cursor := ""
	for {
		page, err := t.Tx.ListTasks(t.ctx, x.ProjectID, store.TaskFilter{BoardID: x.BoardID, RunOf: &x.ID}, cursor, listMaxLimit)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Tasks...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// stripFromBlockers removes id from the blocks cache of each of its blockers outside skip and bumps
// each once (a cache change is a change to the task).
func (t *txn) stripFromBlockers(id string, skip map[string]bool) error {
	edges, err := t.Tx.ListBlockers(t.ctx, t.ProjectID, id)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range edges {
		if e.Type != store.DependencyBlocks || seen[e.FromTaskID] || skip[e.FromTaskID] {
			continue
		}
		seen[e.FromTaskID] = true
		b, err := t.Tx.GetTask(t.ctx, t.ProjectID, e.FromTaskID)
		if err != nil {
			return err
		}
		if !slices.Contains(b.Blocks, id) {
			continue
		}
		if err := t.Tx.SetCaches(t.ctx, b.ProjectID, b.ID, b.BlockedBy, slices.DeleteFunc(slices.Clone(b.Blocks), func(x string) bool { return x == id })); err != nil {
			return err
		}
		after, err := t.bump(b)
		if err != nil {
			return err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: after.ID, BoardID: after.BoardID})
	}
	return nil
}

// deleteAttachments deletes x's attachment rows and returns the blob hashes they held.
// ponytail: the engines' Attachments are unbuilt until R1.8 and the schema cascades their rows with
// the task, so an unbuilt R1.8 answer is no rows; delete this tolerance when R1.8 lands.
func (t *txn) deleteAttachments(x store.Task) ([]string, error) {
	atts, err := t.Tx.ListAttachments(t.ctx, x.ProjectID, x.ID)
	if task, unbuilt := store.UnbuiltTask(err); unbuilt && task == "R1.8" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hashes []string
	for _, a := range atts {
		if a.BlobSHA256 != "" && !slices.Contains(hashes, a.BlobSHA256) {
			hashes = append(hashes, a.BlobSHA256)
		}
	}
	if _, err := t.Tx.DeleteAttachmentsByTask(t.ctx, x.ProjectID, x.ID); err != nil {
		return nil, err
	}
	return hashes, nil
}

// settleRace is cancel_runs's parent write (conductor runs.ts:540-555) when a run of parentID was
// deleted and none is left: racing false, raceOpenedAt cleared, TODO in the board's TODO column,
// the lease dropped. Event run.discarded {runId, reason: "deleted"} on the parent whenever it is
// racing. The parent moves two versions when it was not TODO (UpdateTask, then SetStatus): no store
// call writes racing and status together.
func (t *txn) settleRace(parentID, runID string) error {
	p, err := t.Tx.GetTask(t.ctx, t.ProjectID, parentID)
	if err != nil {
		return err
	}
	if !p.Racing {
		return nil
	}
	data, err := json.Marshal(map[string]any{"runId": runID, "reason": "deleted"})
	if err != nil {
		return err
	}
	t.Event(store.EventInput{TaskID: p.ID, BoardID: p.BoardID, Type: domain.EventRunDiscarded, Data: data})
	left, err := t.Tx.ListTasks(t.ctx, p.ProjectID, store.TaskFilter{BoardID: p.BoardID, RunOf: &p.ID}, "", 1)
	if err != nil {
		return err
	}
	if len(left.Tasks) > 0 {
		t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: p.ID, BoardID: p.BoardID})
		return nil
	}
	off := false
	after, err := t.Tx.UpdateTask(t.ctx, p.ProjectID, p.ID, p.Version, store.TaskPatch{
		Racing: &off, RaceOpenedAt: &store.Nullable[int64]{}, UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli(),
	})
	if err != nil {
		return err
	}
	if after.Status != domain.Todo || after.Claim != nil {
		board, _, err := t.Tx.GetBoard(t.ctx, p.ProjectID, p.BoardID)
		if err != nil {
			return err
		}
		if after, err = t.Tx.SetStatus(t.ctx, p.ProjectID, p.ID, after.Version, store.StatusChange{
			Status: domain.Todo, ColumnID: todoColumn(board), ClearClaim: true, UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli(),
		}); err != nil {
			return err
		}
	}
	bells := doorbellsFor(&p, after, false)
	if len(bells) == 0 && after.Status == domain.Todo && after.Assignee == nil {
		// Back in the claim pool with nobody named: cancel_runs's frontier signal (runs.ts:556-558).
		bells = []Doorbell{{Kind: DoorbellBroadcast, TaskID: p.ID, BoardID: p.BoardID}}
	}
	if len(bells) == 0 {
		bells = []Doorbell{{Kind: ChangeKindChanged, TaskID: p.ID, BoardID: p.BoardID}}
	}
	for _, b := range bells {
		t.Ring(b)
	}
	return nil
}
