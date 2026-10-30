package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// commentsSvc is service.Comments (conductor src/tools/comments.ts).
type commentsSvc struct{ c *Core }

var _ Comments = commentsSvc{}

// Comments is the comment service.
func (c *Core) Comments() Comments { return commentsSvc{c} }

// maxCommentChars is add_comment's and add_follow_up's body bound.
const maxCommentChars = 20_000

func commentOut(c store.Comment) Comment {
	return Comment{Author: c.Author, Body: c.Body, ID: c.ID, Mentions: nonNil(c.Mentions), ParentCommentID: c.ParentCommentID,
		TaskID: c.TaskID, Ts: isoMs(c.TS)}
}

// liveWorkers is this device's registered worker sessions (R0.25's directory, empty until serve
// hands it over): each identity with one, and the agent labels its workers registered under.
func (c *Core) liveWorkers() map[string][]string {
	d := c.DeviceSessions()
	if d == nil {
		return nil
	}
	out := map[string][]string{}
	for _, e := range d.Entries() {
		if e.Role != RoleWorker {
			continue
		}
		labels := out[e.IdentityID]
		if e.AgentID != "" && !strings.HasPrefix(e.AgentID, "id:") { // "id:<identity>" is no label
			labels = append(labels, e.AgentID)
		}
		out[e.IdentityID] = labels
	}
	return out
}

// mentionsOf resolves the body's @mentions against the project's members: a member's identity, its
// account's email and display name, and the agent label of each of its live worker sessions
// (workers) that the member owns (ownedLabels). A body with no @token reads nothing; otherwise one
// member list, one account read per member (conductor's memberAliases, bounded by the member
// count), and one token read when a live worker has a label.
func (t *txn) mentionsOf(body string, workers map[string][]string) ([]string, error) {
	if len(domain.MentionTokens(body)) == 0 {
		return []string{}, nil
	}
	members, err := t.Tx.ListMembers(t.ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(members))
	names := map[string][]string{} // each member's own names: its identity, email and display name
	for _, m := range members {
		ids = append(ids, m.IdentityID)
		names[m.IdentityID] = []string{m.IdentityID}
		u, err := t.Tx.GetUser(t.ctx, m.IdentityID)
		if errors.Is(err, store.ErrNotFound) {
			continue // not an account: the member keeps matching on its identity and labels alone
		}
		if err != nil {
			return nil, err
		}
		names[m.IdentityID] = append(names[m.IdentityID], u.Email, u.DisplayName)
	}
	workers, err = t.ownedLabels(body, workers, names)
	if err != nil {
		return nil, err
	}
	aliases := make(map[string][]string, len(ids))
	for _, id := range ids {
		aliases[id] = append(slices.Clone(workers[id]), names[id][1:]...)
	}
	return domain.ResolveMentions(body, ids, aliases), nil
}

// ownedLabels is workers with only the labels that may alias their identity. A label is its first
// minter's, revoked or expired rows included (P-257,
// REVIEW-257 R2-Q1): one another identity's token row carries never aliases (P-343, Fable B1 on
// #78), and one only its own identity's rows carry aliases as before. A name no token carries is
// dropped when a body token that names it (domain.MentionNames, ResolveMentions' own match) also
// names another identity's token label (P-348, Fable A1/A2) or another member's own name in names,
// its identity, email or display name (P-349, Fable F1). Every identity keeps its live worker (the
// ring by @login). The token rows are read only when a live worker has a label.
func (t *txn) ownedLabels(body string, workers, names map[string][]string) (map[string][]string, error) {
	labelled := false
	for _, ls := range workers {
		labelled = labelled || len(ls) > 0
	}
	if !labelled {
		return workers, nil
	}
	tokens, err := t.Tx.ListTokens(t.ctx, "")
	if err != nil {
		return nil, err
	}
	holders := map[string][]string{} // each token label, with the identities whose rows carry it
	for _, r := range tokens {
		if a := domain.TokenAgent(r.Label); a != "" && !slices.Contains(holders[a], r.IdentityID) {
			holders[a] = append(holders[a], r.IdentityID)
		}
	}
	inBody := domain.MentionTokens(body)
	// namesOther is whether tk names a token label, or a member's own name, of another identity
	// than id.
	namesOther := func(tk, id string) bool {
		for a, hs := range holders {
			if domain.MentionNames(tk, a) && slices.ContainsFunc(hs, func(h string) bool { return h != id }) {
				return true
			}
		}
		for m, ns := range names {
			if m != id && slices.ContainsFunc(ns, func(n string) bool { return domain.MentionNames(tk, n) }) {
				return true
			}
		}
		return false
	}
	out := make(map[string][]string, len(workers))
	for id, ls := range workers {
		kept := []string{}
		for _, l := range ls {
			hs := holders[l]
			if slices.ContainsFunc(hs, func(h string) bool { return h != id }) {
				continue // another identity's label
			}
			if len(hs) == 0 && slices.ContainsFunc(inBody, func(tk string) bool { return domain.MentionNames(tk, l) && namesOther(tk, id) }) {
				continue // a free name this body's token also reads as another identity's
			}
			kept = append(kept, l)
		}
		out[id] = kept
	}
	return out, nil
}

// AddComment is add_comment. The task's version does not move (conductor's addComment writes beside
// the task). Mentions are recorded on the comment, and its comment.added event writes each mentioned
// member a mention notification (notify). A comment rings nobody (ADR-0004), but for a mention
// (owner decision 5, P-310): a mentioned member with a live worker session on this device, named by
// @login or by that worker's @label, is rung assigned on the task. A member with no worker (a human
// at the web UI) and a name that resolves to no member ring nobody, and a self-mention rings nobody
// (RingAssigned).
func (s commentsSvc) AddComment(ctx context.Context, caller domain.Caller, req AddCommentReq) (AddCommentResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_comment", domain.GrantSubject{}, true)
	if err != nil {
		return AddCommentResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return AddCommentResp{}, err
	}
	if err := required("body", req.Body); err != nil {
		return AddCommentResp{}, err
	}
	if err := maxChars("body", req.Body, maxCommentChars); err != nil {
		return AddCommentResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddCommentResp{}, err
	}
	// TODO(spec): conductor's enforceTokenBudget warnings (the per-task chars budget) are not ported.
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_comment", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a, KeepVersion: true,
	}, func(t *txn) (AddCommentResp, error) {
		workers := s.c.liveWorkers()
		mentions, err := t.mentionsOf(req.Body, workers)
		if err != nil {
			return AddCommentResp{}, err
		}
		var parent *string
		if req.ParentCommentID != "" {
			parent = &req.ParentCommentID
		}
		c, err := t.Tx.AddComment(ctx, store.AddCommentInput{ProjectID: req.ProjectID, TaskID: req.TaskID, Author: caller.Identity,
			Body: req.Body, ParentCommentID: parent, Mentions: mentions})
		if err != nil {
			return AddCommentResp{}, err
		}
		data, err := json.Marshal(map[string]any{"commentId": c.ID, "parentCommentId": c.ParentCommentID, "mentions": c.Mentions})
		if err != nil {
			return AddCommentResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventCommentAdded, Data: data})
		for _, id := range c.Mentions {
			if _, live := workers[id]; live {
				t.RingAssigned(id, *t.Task)
			}
		}
		return AddCommentResp{Comment: commentOut(c)}, nil
	})
}

// listLimit is a list tool's limit: 0 is the store's default, else 1 to 200.
func listLimit(limit int64) error {
	if limit < 0 || limit > 200 {
		return domain.Validation("limit is 1 to 200", map[string]any{"field": "limit"})
	}
	return nil
}

// ListComments is list_comments: one statement, oldest first.
func (s commentsSvc) ListComments(ctx context.Context, caller domain.Caller, req ListCommentsReq) (ListCommentsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_comments", domain.GrantSubject{}, false); err != nil {
		return ListCommentsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ListCommentsResp{}, err
	}
	if err := listLimit(req.Limit); err != nil {
		return ListCommentsResp{}, err
	}
	page, err := s.c.store.ListComments(ctx, req.ProjectID, req.TaskID, store.CommentsQuery{Limit: int(req.Limit), Cursor: req.Cursor})
	if err != nil {
		return ListCommentsResp{}, s.c.mapErr("list_comments", err)
	}
	out := ListCommentsResp{Comments: make([]Comment, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, c := range page.Items {
		out.Comments = append(out.Comments, commentOut(c))
	}
	return out, nil
}
