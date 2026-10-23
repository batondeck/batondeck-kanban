package service

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// followUpsSvc is service.FollowUps (conductor src/tools/followups.ts).
type followUpsSvc struct{ c *Core }

var _ FollowUps = followUpsSvc{}

// FollowUps is the follow-up service.
func (c *Core) FollowUps() FollowUps { return followUpsSvc{c} }

func followUpOut(f store.FollowUp) FollowUp {
	return FollowUp{Author: f.Author, Body: f.Body, ID: f.ID, ProjectID: f.ProjectID, Reopened: f.Reopened,
		Status: FollowUpStatus(f.Status), Target: f.Target, TaskID: f.TaskID, Ts: isoMs(f.TS)}
}

// memberOrNil is conductor's notifyTarget: candidate when it is a member of the project, else nil.
func (t *txn) memberOrNil(candidate string) (*string, error) {
	if _, err := t.Tx.GetMember(t.ctx, t.ProjectID, candidate); err != nil {
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &candidate, nil
}

// liveHolder is the identity holding task's lease, or "" when it is unheld or its lease expired
// (leases expire on read, never swept).
func liveHolder(task store.Task, nowMs int64) string {
	if task.Claim != nil && task.Claim.ExpiresAt > nowMs {
		return task.Claim.Owner
	}
	return ""
}

// followUpTarget is conductor's resolveTarget: a live lease's holder (its agent id, else its owner;
// the verified identity is the owner when a member), else the assignee (verified when a member),
// else nobody.
func (t *txn) followUpTarget(task store.Task) (target, identity *string, err error) {
	if owner := liveHolder(task, t.Now.UnixMilli()); owner != "" {
		h := owner
		if task.Claim.AgentID != nil {
			h = *task.Claim.AgentID
		}
		identity, err = t.memberOrNil(owner)
		return &h, identity, err
	}
	if task.Assignee == nil {
		return nil, nil, nil
	}
	a := *task.Assignee
	identity, err = t.memberOrNil(a)
	return &a, identity, err
}

// targetIsCaller is conductor's targetIsCaller: the verified identity is the caller's, and the
// routing handle is that identity or the caller's agent (which narrows within one identity only).
func targetIsCaller(caller domain.Caller, target string, identity *string) bool {
	if identity == nil || *identity != caller.Identity {
		return false
	}
	return target == *identity || (caller.Agent != "" && target == caller.Agent)
}

// AddFollowUp is add_follow_up, in one transaction: the follow-up, its event, the reopen when the
// task is in REVIEW or DONE and reopen is set, and the doorbell. Without a reopen the task's version
// does not move (conductor's addFollowUp writes beside the task); a reopen moves it once.
func (s followUpsSvc) AddFollowUp(ctx context.Context, caller domain.Caller, req AddFollowUpReq) (AddFollowUpResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_follow_up", domain.GrantSubject{}, true)
	if err != nil {
		return AddFollowUpResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return AddFollowUpResp{}, err
	}
	if err := required("body", req.Body); err != nil {
		return AddFollowUpResp{}, err
	}
	if err := maxChars("body", req.Body, maxCommentChars); err != nil {
		return AddFollowUpResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddFollowUpResp{}, err
	}
	// TODO(spec): conductor's enforceTokenBudget warnings (the per-task chars budget) are not ported.
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_follow_up", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a, KeepVersion: true,
	}, func(t *txn) (AddFollowUpResp, error) {
		pre := *t.Task
		target, identity, err := t.followUpTarget(pre)
		if err != nil {
			return AddFollowUpResp{}, err
		}
		if target != nil && targetIsCaller(caller, *target, identity) {
			target, identity = nil, nil // a directive to yourself notifies nobody
		}
		reopen := req.Reopen && (pre.Status == domain.Review || pre.Status == domain.Done)
		f, err := t.Tx.AddFollowUp(ctx, store.AddFollowUpInput{ProjectID: req.ProjectID, TaskID: req.TaskID, Body: req.Body,
			Author: caller.Identity, Target: target, TargetIdentity: identity, Reopened: reopen})
		if err != nil {
			return AddFollowUpResp{}, err
		}
		data, err := json.Marshal(map[string]any{"followUpId": f.ID, "targetIdentity": identity, "reopened": reopen})
		if err != nil {
			return AddFollowUpResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventTaskFollowUp, Data: data})
		after := pre
		if reopen {
			// An automatic, past the transition table (conductor's reopen), to TODO (ADR-0005): the
			// task holds no lease in REVIEW or DONE, and its assignee is kept so the target is re-rung.
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, pre.BoardID)
			if err != nil {
				return AddFollowUpResp{}, err
			}
			if after, err = t.Tx.SetStatus(ctx, req.ProjectID, req.TaskID, pre.Version, store.StatusChange{
				Status: domain.Todo, ColumnID: todoColumn(board), UpdatedBy: caller.Identity, UpdatedAt: t.Now.UnixMilli(),
			}); err != nil {
				return AddFollowUpResp{}, err
			}
			t.Wrote(after)
			data, err := json.Marshal(map[string]any{"from": pre.Status, "to": domain.Todo})
			if err != nil {
				return AddFollowUpResp{}, err
			}
			t.Event(store.EventInput{Type: domain.EventTaskReopened, Data: data})
			t.RingFor(&pre, after, false) // an unassigned TODO is open work: broadcast
		}
		switch {
		case identity != nil:
			t.RingAssigned(*identity, after)
		case target != nil:
			t.RingAssigned(*target, after) // an agent label: its sessions' scope
		}
		out, err := withSummary(ctx, t.Tx, after)
		if err != nil {
			return AddFollowUpResp{}, err
		}
		return AddFollowUpResp{FollowUp: followUpOut(f), Task: out}, nil
	})
}

// AckFollowUp is ack_follow_up. The task's version does not move.
func (s followUpsSvc) AckFollowUp(ctx context.Context, caller domain.Caller, req AckFollowUpReq) (AckFollowUpResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "ack_follow_up", domain.GrantSubject{}, true)
	if err != nil {
		return AckFollowUpResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return AckFollowUpResp{}, err
	}
	if err := required("followUpId", req.FollowUpID); err != nil {
		return AckFollowUpResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "ack_follow_up", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Access: a, KeepVersion: true,
	}, func(t *txn) (AckFollowUpResp, error) {
		f, err := t.Tx.GetFollowUp(ctx, req.ProjectID, req.TaskID, req.FollowUpID)
		if err != nil {
			return AckFollowUpResp{}, err
		}
		if f.Target != nil && a.Role != domain.RoleAdmin {
			refused := forbidden("Only the follow-up target (or a project admin) can acknowledge it", map[string]any{"followUpId": f.ID})
			if f.TargetIdentity != nil {
				if !targetIsCaller(caller, *f.Target, f.TargetIdentity) {
					return AckFollowUpResp{}, refused
				}
			} else if liveHolder(*t.Task, t.Now.UnixMilli()) != caller.Identity {
				// Bound to no identity (an agent label): holding the task's live lease is the proof.
				return AckFollowUpResp{}, refused
			}
		}
		acked, err := t.Tx.AckFollowUp(ctx, req.ProjectID, req.TaskID, req.FollowUpID)
		if err != nil {
			return AckFollowUpResp{}, err
		}
		data, err := json.Marshal(map[string]any{"followUpId": f.ID})
		if err != nil {
			return AckFollowUpResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventFollowupAcked, Data: data})
		return AckFollowUpResp{FollowUp: followUpOut(acked)}, nil
	})
}

// ListFollowUps is list_follow_ups: one statement, oldest first.
func (s followUpsSvc) ListFollowUps(ctx context.Context, caller domain.Caller, req ListFollowUpsReq) (ListFollowUpsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_follow_ups", domain.GrantSubject{}, false); err != nil {
		return ListFollowUpsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ListFollowUpsResp{}, err
	}
	if err := listLimit(req.Limit); err != nil {
		return ListFollowUpsResp{}, err
	}
	page, err := s.c.store.ListFollowUps(ctx, req.ProjectID, req.TaskID, store.FollowUpsQuery{Limit: int(req.Limit), Cursor: req.Cursor, OpenOnly: req.OpenOnly})
	if err != nil {
		return ListFollowUpsResp{}, s.c.mapErr("list_follow_ups", err)
	}
	out := ListFollowUpsResp{FollowUps: make([]FollowUp, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, f := range page.Items {
		out.FollowUps = append(out.FollowUps, followUpOut(f))
	}
	return out, nil
}
