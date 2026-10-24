package service

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// artifactsSvc is service.Artifacts (conductor src/tools/tasks.ts add_artifact, retract_artifact
// and src/domain/artifacts.ts).
type artifactsSvc struct{ c *Core }

var _ Artifacts = artifactsSvc{}

// Artifacts is the artifact service.
func (c *Core) Artifacts() Artifacts { return artifactsSvc{c} }

// maxArtifactsPerTask is conductor's MAX_ARTIFACTS_PER_TASK, tombstones included.
const maxArtifactsPerTask = 50

// Conductor's ArtifactInputSchema bounds (T-197), in JavaScript string length.
var artifactFieldMax = map[string]int{"url": 1000, "ref": 200, "repo": 200, "title": 200}

// artifactKey is conductor's keyOf: kind, the url case-folded and the ref. repo is not part of it,
// which is what lets retract_artifact's repo tell a corrected entry from the wrong one.
func artifactKey(kind ArtifactKind, url, ref *string) string {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return string(kind) + "|" + strings.ToLower(deref(url)) + "|" + deref(ref)
}

func trimmedPtr(field, v string) (*string, error) {
	v = strings.TrimSpace(v)
	if err := maxChars(field, v, artifactFieldMax[field]); err != nil {
		return nil, err
	}
	if v == "" {
		return nil, nil
	}
	return &v, nil
}

// normalizeArtifact is conductor's normalizeArtifacts for one entry: trimmed, the url an http(s)
// URL, a url or a ref present, the author and agent the caller's.
func normalizeArtifact(in ArtifactInput, projectID, taskID string, caller domain.Caller) (store.AddArtifactInput, error) {
	out := store.AddArtifactInput{ProjectID: projectID, TaskID: taskID, Kind: store.ArtifactKind(in.Kind), AddedBy: caller.Identity, AddedByAgent: callerAgent(caller)}
	if !store.ArtifactKinds[out.Kind] {
		return out, domain.Validation("unknown artifact kind: "+string(in.Kind), map[string]any{"field": "kind"})
	}
	var err error
	for _, f := range []struct {
		name string
		v    string
		into **string
	}{{"url", in.URL, &out.URL}, {"ref", in.Ref, &out.Ref}, {"repo", in.Repo, &out.Repo}, {"title", in.Title, &out.Title}} {
		if *f.into, err = trimmedPtr(f.name, f.v); err != nil {
			return out, err
		}
	}
	if out.URL == nil && out.Ref == nil {
		return out, domain.Validation("artifact needs a url or a ref (branch / commit sha / repo-relative path)", nil)
	}
	if out.URL != nil && !safeHTTPURL(*out.URL) {
		return out, domain.Validation("artifact url must be an http(s) URL", map[string]any{"url": *out.URL})
	}
	return out, nil
}

func artifactOut(a store.Artifact) Artifact {
	out := Artifact{AddedBy: a.AddedBy, AddedByAgent: a.AddedByAgent, Kind: ArtifactKind(a.Kind), Ref: a.Ref, Repo: a.Repo,
		Title: a.Title, Ts: isoMs(a.TS), URL: a.URL}
	if r := a.Retraction; r != nil {
		out.Retracted = &ArtifactRetraction{At: isoMs(r.At), By: r.By, ByAgent: r.ByAgent, Reason: r.Reason}
	}
	return out
}

// withArtifacts is task with its artifacts read (one statement): the add and retract answers and
// get_task carry them, as conductor's task document does.
func withArtifacts(ctx context.Context, st store.Store, out Task) (Task, error) {
	list, err := st.ListArtifacts(ctx, out.ProjectID, out.ID)
	if err != nil {
		return Task{}, err
	}
	out.Artifacts = make([]Artifact, 0, len(list))
	for _, a := range list {
		out.Artifacts = append(out.Artifacts, artifactOut(a))
	}
	return out, nil
}

func versionOf(v Optional[int64]) *int64 {
	if !v.Set {
		return nil
	}
	n := v.V
	return &n
}

// taskWithContext is the task after a write, with its summary and artifacts.
func (t *txn) taskWithContext(after store.Task) (Task, error) {
	out, err := withSummary(t.ctx, t.Tx, after)
	if err != nil {
		return Task{}, err
	}
	return withArtifacts(t.ctx, t.Tx, out)
}

// AddArtifact is add_artifact: the entries are de-duplicated against the task's live ones and each
// other, the cap counts tombstones (conductor's merged.length), and the version moves once even when
// every entry was a duplicate, as conductor's txn.set does.
func (s artifactsSvc) AddArtifact(ctx context.Context, caller domain.Caller, req AddArtifactReq) (AddArtifactResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_artifact", domain.GrantSubject{}, true)
	if err != nil {
		return AddArtifactResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return AddArtifactResp{}, err
	}
	if n := len(req.Artifacts); n == 0 || n > maxArtifactsPerTask {
		return AddArtifactResp{}, domain.Validation("artifacts holds 1 to 50 entries", map[string]any{"field": "artifacts"})
	}
	incoming := make([]store.AddArtifactInput, 0, len(req.Artifacts))
	for _, in := range req.Artifacts {
		n, err := normalizeArtifact(in, req.ProjectID, req.TaskID, caller)
		if err != nil {
			return AddArtifactResp{}, err
		}
		incoming = append(incoming, n)
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_artifact", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: versionOf(req.Version), Access: a,
	}, func(t *txn) (AddArtifactResp, error) {
		have, err := t.Tx.ListArtifacts(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return AddArtifactResp{}, err
		}
		seen := map[string]bool{}
		for _, x := range have {
			if x.Retraction == nil {
				seen[artifactKey(ArtifactKind(x.Kind), x.URL, x.Ref)] = true
			}
		}
		var fresh []store.AddArtifactInput
		kinds := make([]string, 0, len(incoming))
		for _, in := range incoming {
			kinds = append(kinds, string(in.Kind))
			k := artifactKey(ArtifactKind(in.Kind), in.URL, in.Ref)
			if !seen[k] {
				seen[k] = true
				fresh = append(fresh, in)
			}
		}
		if len(have)+len(fresh) > maxArtifactsPerTask {
			return AddArtifactResp{}, domain.Validation("Task "+req.TaskID+" would exceed 50 artifacts", map[string]any{"have": len(have), "adding": len(incoming)})
		}
		for _, in := range fresh {
			if _, err := t.Tx.AddArtifact(ctx, in); err != nil {
				return AddArtifactResp{}, err
			}
		}
		data, err := json.Marshal(map[string]any{"kinds": kinds, "added": len(fresh)})
		if err != nil {
			return AddArtifactResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventTaskArtifactAdded, Data: data})
		after, err := t.Bump()
		if err != nil {
			return AddArtifactResp{}, err
		}
		out, err := t.taskWithContext(after)
		if err != nil {
			return AddArtifactResp{}, err
		}
		return AddArtifactResp{Added: int64(len(fresh)), Task: out}, nil
	})
}

// RetractArtifact is retract_artifact: every live entry the selector matches (kind, and each given
// url case-folded, ref and repo) is tombstoned; a selector matching only tombstones is a safe retry
// (retracted 0), one matching nothing at all is NOT_FOUND.
func (s artifactsSvc) RetractArtifact(ctx context.Context, caller domain.Caller, req RetractArtifactReq) (RetractArtifactResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "retract_artifact", domain.GrantSubject{}, true)
	if err != nil {
		return RetractArtifactResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return RetractArtifactResp{}, err
	}
	sel := req.Artifact
	if !store.ArtifactKinds[store.ArtifactKind(sel.Kind)] {
		return RetractArtifactResp{}, domain.Validation("unknown artifact kind: "+string(sel.Kind), map[string]any{"field": "artifact.kind"})
	}
	if strings.TrimSpace(sel.URL) == "" && strings.TrimSpace(sel.Ref) == "" {
		return RetractArtifactResp{}, domain.Validation("artifact selector needs a url or a ref to identify what to retract", nil)
	}
	for f, v := range map[string]string{"url": sel.URL, "ref": sel.Ref, "repo": sel.Repo} {
		if err := maxChars(f, v, artifactFieldMax[f]); err != nil {
			return RetractArtifactResp{}, err
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return RetractArtifactResp{}, domain.Validation("reason is required", map[string]any{"field": "reason"})
	}
	if err := maxChars("reason", reason, 500); err != nil {
		return RetractArtifactResp{}, err
	}
	matches := func(x store.Artifact) bool {
		eq := func(p *string, v string) bool { return p != nil && *p == v }
		return string(x.Kind) == string(sel.Kind) &&
			(sel.URL == "" || (x.URL != nil && strings.EqualFold(*x.URL, sel.URL))) &&
			(sel.Ref == "" || eq(x.Ref, sel.Ref)) && (sel.Repo == "" || eq(x.Repo, sel.Repo))
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "retract_artifact", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: versionOf(req.Version), Access: a,
	}, func(t *txn) (RetractArtifactResp, error) {
		have, err := t.Tx.ListArtifacts(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return RetractArtifactResp{}, err
		}
		r := store.ArtifactRetraction{By: caller.Identity, ByAgent: callerAgent(caller), At: t.Now.UnixMilli(), Reason: reason}
		matched, retracted := 0, 0
		for _, x := range have {
			if !matches(x) {
				continue
			}
			matched++
			if x.Retraction != nil {
				continue
			}
			if _, err := t.Tx.RetractArtifact(ctx, req.ProjectID, req.TaskID, x.ID, r); err != nil {
				return RetractArtifactResp{}, err
			}
			retracted++
		}
		if matched == 0 {
			return RetractArtifactResp{}, domain.NotFound("Artifact", string(sel.Kind)+" "+cmp.Or(sel.URL, sel.Ref)+" on "+req.TaskID)
		}
		data, err := json.Marshal(map[string]any{"kind": sel.Kind, "retracted": retracted, "reason": reason})
		if err != nil {
			return RetractArtifactResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventTaskArtifactRetracted, Data: data})
		after, err := t.Bump()
		if err != nil {
			return RetractArtifactResp{}, err
		}
		out, err := t.taskWithContext(after)
		if err != nil {
			return RetractArtifactResp{}, err
		}
		return RetractArtifactResp{Retracted: int64(retracted), Task: out}, nil
	})
}
