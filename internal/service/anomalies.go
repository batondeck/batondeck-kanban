package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Anomalies is the stuck-work sensor service.
func (c *Core) Anomalies() Anomalies { return anomaliesSvc{c} }

type anomaliesSvc struct{ c *Core }

const (
	// maxLookbackSec is sinceSec's ceiling (conductor's MAX_LOOKBACK_SEC, 30 days).
	maxLookbackSec = 30 * 24 * 3600
	// eventTail is the feed tail read, one page (the store's largest).
	eventTail = 200
	// boardPages bounds the board read to conductor's MAX_QUERY_LIMIT, 1000 tasks: at most five
	// pages of 200.
	boardPages = 5
	// maxAnomalyEvents caps the first-sighting appends of one call (conductor's MAX_APPEND_EVENTS);
	// the rest are appended by the next call.
	maxAnomalyEvents = 50
)

// boardTasks is the board's tasks, at most boardPages pages.
func boardTasks(ctx context.Context, st store.Store, projectID string, f store.TaskFilter) ([]store.Task, error) {
	var out []store.Task
	cursor := ""
	for range boardPages {
		page, err := st.ListTasks(ctx, projectID, f, cursor, 200)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Tasks...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	return out, nil
}

// anomalyPolicy is the project's anomalyPolicy, a zero threshold reading as the default's.
func anomalyPolicy(s Settings) domain.AnomalyPolicy {
	p := s.AnomalyPolicy
	return domain.PolicyOrDefault(domain.AnomalyPolicy{
		StaleInProgress: time.Duration(p.StaleInProgressMs) * time.Millisecond, ClaimThrashCount: p.ClaimThrashCount,
		ReworkLoopCount: p.ReworkLoopCount, WIPJamMinTasks: p.WIPJamMinTasks,
		StarvedUrgent: time.Duration(p.StarvedUrgentMs) * time.Millisecond, Window: time.Duration(p.WindowSec) * time.Second,
	})
}

func anomalyTask(t store.Task) domain.AnomalyTask {
	a := domain.AnomalyTask{ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority, Assignee: t.Assignee,
		RequiredCapabilities: t.RequiredCapabilities, BlockedBy: t.BlockedBy, ClaimAttempts: t.ClaimAttempts, Racing: t.Racing,
		CreatedAt: time.UnixMilli(t.CreatedAt), UpdatedAt: time.UnixMilli(t.UpdatedAt)}
	if t.BlockedReason != nil {
		a.BlockedReason = *t.BlockedReason
	}
	if t.NextEligibleAt != nil {
		a.NextEligibleAt = time.UnixMilli(*t.NextEligibleAt)
	}
	if t.Claim != nil {
		exp := time.UnixMilli(t.Claim.ExpiresAt)
		a.ClaimOwner, a.ClaimAgent, a.ClaimExpiresAt = t.Claim.Owner, t.Claim.Agent, &exp
	}
	return a
}

// Detect is detect_anomalies: the board's task pages (at most five) and one feed page, then at
// most one transaction for the first sightings.
func (s anomaliesSvc) Detect(ctx context.Context, caller domain.Caller, req DetectAnomaliesReq) (DetectAnomaliesResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "detect_anomalies", domain.GrantSubject{}, false)
	if err != nil {
		return DetectAnomaliesResp{}, err
	}
	if req.Kinds != nil && len(req.Kinds) == 0 {
		return DetectAnomaliesResp{}, domain.Validation("kinds needs at least one kind", map[string]any{"field": "kinds"})
	}
	for _, k := range req.Kinds {
		if !slices.Contains(AnomalyKindValues, k) {
			return DetectAnomaliesResp{}, domain.Validation("unknown anomaly kind: "+string(k), map[string]any{"field": "kinds"})
		}
	}
	if req.SinceSec < 0 || req.SinceSec > maxLookbackSec {
		return DetectAnomaliesResp{}, domain.Validation("sinceSec is 1 to 2592000", map[string]any{"field": "sinceSec"})
	}
	boardID := req.BoardID
	if boardID == "" {
		if req.TaskID == "" {
			return DetectAnomaliesResp{}, domain.Validation("Provide boardId (or taskId): a scan is scoped to one board", map[string]any{"field": "boardId"})
		}
		t, err := s.c.store.GetTask(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return DetectAnomaliesResp{}, s.c.mapErr("detect_anomalies", err)
		}
		boardID = t.BoardID
	}
	proj, err := s.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return DetectAnomaliesResp{}, s.c.mapErr("detect_anomalies", err)
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return DetectAnomaliesResp{}, s.c.mapErr("detect_anomalies", err)
	}
	tasks, err := boardTasks(ctx, s.c.store, req.ProjectID, store.TaskFilter{BoardID: boardID})
	if err != nil {
		return DetectAnomaliesResp{}, s.c.mapErr("detect_anomalies", err)
	}
	feed, err := s.c.store.ListBoardFeed(ctx, req.ProjectID, boardID, "", eventTail)
	if err != nil {
		return DetectAnomaliesResp{}, s.c.mapErr("detect_anomalies", err)
	}
	in := domain.DetectInput{Now: s.c.now(), Policy: anomalyPolicy(settings), TaskID: req.TaskID, EventLimit: eventTail,
		Since: time.Duration(req.SinceSec) * time.Second}
	for _, k := range req.Kinds {
		in.Kinds = append(in.Kinds, domain.AnomalyKind(k))
	}
	for _, t := range tasks {
		in.Tasks = append(in.Tasks, anomalyTask(t))
	}
	for _, e := range feed.Events {
		ae := domain.AnomalyEvent{Type: e.Type, TS: time.UnixMilli(e.Ts), Data: e.Data}
		if e.TaskID != nil {
			ae.TaskID = *e.TaskID
		}
		in.Events = append(in.Events, ae)
	}
	found, scan := domain.DetectAnomalies(in)
	if fresh := domain.FirstSightings(found, in.Events); len(fresh) > 0 {
		s.record(ctx, caller, a, req.ProjectID, boardID, fresh[:min(len(fresh), maxAnomalyEvents)])
	}
	out := DetectAnomaliesResp{Anomalies: make([]Anomaly, 0, len(found)), BoardID: boardID, Scanned: AnomalyScope{
		EventsScanned: int64(scan.EventsScanned), TasksScanned: int64(scan.TasksScanned), Truncated: scan.Truncated, WindowFrom: isoMs(scan.WindowFrom.UnixMilli())}}
	if scan.OldestEventTS != nil {
		o := isoMs(scan.OldestEventTS.UnixMilli())
		out.Scanned.OldestEventTS = &o
	}
	for _, f := range found {
		an := Anomaly{DetectedAt: isoMs(f.DetectedAt.UnixMilli()), Evidence: f.Evidence, Kind: AnomalyKind(f.Kind), Severity: AnomalySeverity(f.Severity)}
		if f.TaskID != "" {
			id := f.TaskID
			an.TaskID = &id
		}
		out.Anomalies = append(out.Anomalies, an)
	}
	return out, nil
}

// record appends task.anomaly_detected for each first sighting, in one transaction, best-effort: a
// sensor never fails the caller's read because its trail write did (conductor logs and goes on).
func (s anomaliesSvc) record(ctx context.Context, caller domain.Caller, a access, projectID, boardID string, fresh []domain.Anomaly) {
	_, err := mutate(ctx, s.c, caller, mutation{
		Tool: "detect_anomalies", Args: map[string]any{"projectId": projectID, "boardId": boardID}, ProjectID: projectID, Access: a,
	}, func(t *txn) (struct{}, error) {
		for _, f := range fresh {
			data, err := json.Marshal(map[string]any{"kind": f.Kind, "severity": f.Severity, "evidence": f.Evidence})
			if err != nil {
				return struct{}{}, err
			}
			e := store.EventInput{BoardID: boardID, TaskID: f.TaskID, Type: domain.EventTaskAnomalyDetected, Data: data}
			if f.TaskID == "" {
				t.BoardEvent(e)
			} else {
				t.Event(e)
			}
		}
		t.boards = append(t.boards, [2]string{projectID, boardID})
		return struct{}{}, nil
	})
	if err != nil {
		s.c.log.Printf("service: detect_anomalies: the first-sighting events on %s/%s were not appended: %+v", projectID, boardID, err)
	}
}
