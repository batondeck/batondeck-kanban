package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

// Conductor's test/unit/anomaly.test.ts for the rows kept here: every detector a firing case, a quiet
// control and its boundary. Ported rows change only where the plan moved them: READY is TODO
// (ADR-0005), a dead blocker is a poisoned one (warn) and there is no CANCELLED row (plan §Fold 1).

var anNow = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func anAgo(d time.Duration) time.Time { return anNow.Add(-d) }

func anTask(id string, mod ...func(*AnomalyTask)) AnomalyTask {
	t := AnomalyTask{ID: id, Title: id, Status: Todo, Priority: PriorityNormal, CreatedAt: anAgo(time.Hour), UpdatedAt: anAgo(time.Minute)}
	for _, m := range mod {
		m(&t)
	}
	return t
}

// working is IN_PROGRESS under a live lease, idle for idle.
func working(id string, idle time.Duration) AnomalyTask {
	exp := anNow.Add(5 * time.Minute)
	return anTask(id, func(t *AnomalyTask) {
		t.Status, t.UpdatedAt, t.ClaimOwner, t.ClaimExpiresAt = InProgress, anAgo(idle), "a@x", &exp
	})
}

func expired(id string) AnomalyTask {
	exp := anAgo(time.Hour)
	return anTask(id, func(t *AnomalyTask) { t.Status, t.ClaimOwner, t.ClaimExpiresAt = InProgress, "a", &exp })
}

func anEv(typ EventType, task string, data any, ago time.Duration) AnomalyEvent {
	b, _ := json.Marshal(data)
	if data == nil {
		b = []byte("{}")
	}
	return AnomalyEvent{Type: typ, TaskID: task, TS: anAgo(ago), Data: b}
}

func anRun(tasks []AnomalyTask, events []AnomalyEvent, mod ...func(*DetectInput)) []Anomaly {
	in := DetectInput{Tasks: tasks, Events: events, Now: anNow, Policy: DefaultAnomalyPolicy, EventLimit: 200}
	for _, m := range mod {
		m(&in)
	}
	got, _ := DetectAnomalies(in)
	return got
}

func kindsOf(as []Anomaly) []AnomalyKind {
	out := []AnomalyKind{}
	for _, a := range as {
		out = append(out, a.Kind)
	}
	return out
}

func TestPolicyOrDefault(t *testing.T) {
	if got := PolicyOrDefault(AnomalyPolicy{}); got != DefaultAnomalyPolicy {
		t.Fatalf("an all-zero policy (update_settings {anomalyPolicy:{}}) = %+v, want the defaults", got)
	}
	got := PolicyOrDefault(AnomalyPolicy{ClaimThrashCount: 7, StaleInProgress: -1})
	want := DefaultAnomalyPolicy
	want.ClaimThrashCount = 7
	if got != want {
		t.Fatalf("one tuned threshold = %+v, want %+v (field by field)", got, want)
	}
	want = DefaultAnomalyPolicy
	if want.ClaimThrashCount != 3 || want.ReworkLoopCount != 2 || want.WIPJamMinTasks != 2 || want.StaleInProgress != 30*time.Minute ||
		want.StarvedUrgent != 24*time.Hour || want.Window != 6*time.Hour {
		t.Fatalf("defaults %+v are not conductor's", want)
	}
}

func TestStaleInProgress(t *testing.T) {
	if k := kindsOf(anRun([]AnomalyTask{working("T-1", 31*time.Minute)}, nil)); !slices.Equal(k, []AnomalyKind{AnomalyStaleInProgress}) {
		t.Fatalf("idle 31m under a live lease: %v", k)
	}
	if k := anRun([]AnomalyTask{working("T-1", 5*time.Minute)}, nil); len(k) != 0 {
		t.Fatalf("recent progress (control): %v", k)
	}
	if k := anRun([]AnomalyTask{working("T-1", 30*time.Minute)}, nil); len(k) != 1 || k[0].Severity != SeverityWarn {
		t.Fatalf("exactly at the threshold: %v, want one warn", k)
	}
	if k := anRun([]AnomalyTask{working("T-1", time.Hour)}, nil); k[0].Severity != SeverityCritical {
		t.Fatalf("2x the threshold: %v, want critical", k[0].Severity)
	}
	if k := kindsOf(anRun([]AnomalyTask{expired("T-1")}, nil, func(in *DetectInput) { in.Kinds = []AnomalyKind{AnomalyStaleInProgress} })); len(k) != 0 {
		t.Fatalf("an expired lease is the reaper's shape: %v", k)
	}
	noClaim := anTask("T-1", func(t *AnomalyTask) { t.Status, t.UpdatedAt = InProgress, anAgo(time.Hour) })
	if k := anRun([]AnomalyTask{noClaim}, nil, func(in *DetectInput) { in.Kinds = []AnomalyKind{AnomalyStaleInProgress} }); len(k) != 0 {
		t.Fatalf("no claim at all: %v", k)
	}
}

func TestClaimThrash(t *testing.T) {
	claims := func(n int, id string, every time.Duration) []AnomalyEvent {
		var out []AnomalyEvent
		for i := range n {
			out = append(out, anEv(EventTaskClaimed, id, nil, time.Duration(i+1)*every))
		}
		return out
	}
	tk := []AnomalyTask{anTask("T-1")}
	if k := anRun(tk, claims(2, "T-1", time.Minute)); len(k) != 0 {
		t.Fatalf("two claims: %v", k)
	}
	a := anRun(tk, append(claims(3, "T-1", time.Minute), anEv(EventTaskReleased, "T-1", nil, time.Minute)))
	if len(a) != 1 || a[0].Kind != AnomalyClaimThrash || a[0].Evidence["claims"] != int64(3) || a[0].Evidence["releases"] != int64(1) {
		t.Fatalf("three claims: %+v", a)
	}
	if k := anRun(tk, claims(10, "T-1", 48*time.Hour)); len(k) != 0 {
		t.Fatalf("claims outside the window: %v", k)
	}
	old := claims(4, "T-1", 10*time.Hour)
	if k := anRun(tk, old); len(k) != 0 {
		t.Fatalf("10h+ old claims in a 6h window: %v", k)
	}
	if k := kindsOf(anRun(tk, old, func(in *DetectInput) { in.Since = 50 * time.Hour })); !slices.Equal(k, []AnomalyKind{AnomalyClaimThrash}) {
		t.Fatalf("sinceSec widening: %v", k)
	}
	if k := anRun(tk, claims(5, "T-2", time.Minute)); len(k) != 0 {
		t.Fatalf("another task's claims: %v", k)
	}
}

func TestReworkLoop(t *testing.T) {
	back := map[string]any{"from": "REVIEW", "to": "IN_PROGRESS"}
	both := []AnomalyEvent{anEv(EventTaskMoved, "T-1", back, time.Minute), anEv(EventTaskReopened, "T-1", map[string]any{"from": "REVIEW", "to": "TODO"}, time.Minute)}
	if k := kindsOf(anRun([]AnomalyTask{anTask("T-1")}, both)); !slices.Equal(k, []AnomalyKind{AnomalyReworkLoop}) {
		t.Fatalf("a move back and a reopen: %v", k)
	}
	fwd := []AnomalyEvent{anEv(EventTaskMoved, "T-1", map[string]any{"from": "IN_PROGRESS", "to": "REVIEW"}, time.Minute), anEv(EventTaskMoved, "T-1", map[string]any{"from": "REVIEW", "to": "DONE"}, time.Minute)}
	if k := anRun([]AnomalyTask{anTask("T-1")}, fwd); len(k) != 0 {
		t.Fatalf("forward only (control): %v", k)
	}
	if k := anRun([]AnomalyTask{anTask("T-1")}, both[:1]); len(k) != 0 {
		t.Fatalf("one round is not a loop: %v", k)
	}
}

func TestWIPJam(t *testing.T) {
	a := anRun([]AnomalyTask{expired("T-1"), expired("T-2")}, nil)
	if len(a) != 1 || a[0].Kind != AnomalyWIPJam || a[0].TaskID != "" || a[0].Severity != SeverityCritical || a[0].Evidence["remedy"] != "reap_stale_leases" {
		t.Fatalf("every lease expired: %+v", a)
	}
	if k := anRun([]AnomalyTask{expired("T-1"), working("T-2", time.Minute)}, nil); len(k) != 0 {
		t.Fatalf("one live lease (control): %v", k)
	}
	if k := anRun([]AnomalyTask{expired("T-1")}, nil); len(k) != 0 {
		t.Fatalf("one stuck card, under wipJamMinTasks: %v", k)
	}
	if k := anRun(nil, nil); len(k) != 0 {
		t.Fatalf("an empty board: %v", k)
	}
}

func TestStarvedUrgent(t *testing.T) {
	urgent := func(age time.Duration, mod ...func(*AnomalyTask)) AnomalyTask {
		return anTask("T-1", append([]func(*AnomalyTask){func(t *AnomalyTask) { t.Priority, t.CreatedAt = PriorityUrgent, anAgo(age) }}, mod...)...)
	}
	if k := kindsOf(anRun([]AnomalyTask{urgent(25 * time.Hour)}, nil)); !slices.Equal(k, []AnomalyKind{AnomalyStarvedUrgent}) {
		t.Fatalf("an old urgent TODO card: %v", k)
	}
	for name, tk := range map[string]AnomalyTask{
		"fresh":       urgent(time.Hour),
		"non-urgent":  anTask("T-1", func(t *AnomalyTask) { t.Priority, t.CreatedAt = PriorityHigh, anAgo(30*24*time.Hour) }),
		"in backoff":  urgent(25*time.Hour, func(t *AnomalyTask) { t.NextEligibleAt = anNow.Add(time.Hour) }),
		"racing":      urgent(25*time.Hour, func(t *AnomalyTask) { t.Racing = true }),
		"in progress": urgent(25*time.Hour, func(t *AnomalyTask) { t.Status = InProgress }),
	} {
		if k := anRun([]AnomalyTask{tk}, nil, func(in *DetectInput) { in.Kinds = []AnomalyKind{AnomalyStarvedUrgent} }); len(k) != 0 {
			t.Fatalf("%s: %v", name, k)
		}
	}
}

// The severity table keys on blockedReason (plan §Fold 1): a poisoned blocker is warn; a blocker not
// done yet, a DONE one and a cancelled one (DONE with the cancelled label, ADR-0001) are nothing.
func TestDepDeadlock(t *testing.T) {
	dependent := anTask("T-1", func(t *AnomalyTask) { t.Status, t.BlockedBy = Blocked, []string{"T-2"} })
	poisoned := anTask("T-2", func(t *AnomalyTask) { t.Status, t.BlockedReason = Blocked, BlockedReasonPoison })
	a := anRun([]AnomalyTask{dependent, poisoned}, nil)
	if len(a) != 1 || a[0].Kind != AnomalyDepDeadlock || a[0].Severity != SeverityWarn || a[0].TaskID != "T-1" {
		t.Fatalf("behind a poisoned blocker: %+v, want one warn", a)
	}
	dead := a[0].Evidence["deadBlockers"].([]any)[0].(map[string]any)
	if dead["taskId"] != "T-2" || dead["blockedReason"] != "poison" || dead["status"] != Blocked {
		t.Fatalf("the dead blocker %v does not name its blockedReason", dead)
	}
	for name, blocker := range map[string]AnomalyTask{
		"not done yet":     anTask("T-2", func(t *AnomalyTask) { t.Status = InProgress }),
		"blocked, waiting": anTask("T-2", func(t *AnomalyTask) { t.Status, t.BlockedReason = Blocked, "waiting on infra" }),
		"done":             anTask("T-2", func(t *AnomalyTask) { t.Status = Done }),
		"cancelled":        anTask("T-2", func(t *AnomalyTask) { t.Status = Done }),
	} {
		if k := anRun([]AnomalyTask{dependent, blocker}, nil); len(k) != 0 {
			t.Fatalf("a %s blocker: %v", name, k)
		}
	}
	off := anTask("T-1", func(t *AnomalyTask) { t.Status, t.BlockedBy = Blocked, []string{"T-2", "T-9"} })
	a = anRun([]AnomalyTask{off, poisoned}, nil)
	if len(a) != 1 || fmt.Sprint(a[0].Evidence["blockersNotOnPage"]) != "[T-9]" {
		t.Fatalf("a blocker off the page: %+v", a)
	}
}

func TestAnomalyFiltersOrderAndWindow(t *testing.T) {
	tasks := []AnomalyTask{working("T-1", time.Hour), working("T-2", 31*time.Minute)}
	if k := anRun(tasks, nil, func(in *DetectInput) { in.Kinds = []AnomalyKind{AnomalyWIPJam} }); len(k) != 0 {
		t.Fatalf("kinds filter: %v", k)
	}
	if k := anRun(tasks, nil, func(in *DetectInput) { in.TaskID = "T-2" }); len(k) != 1 || k[0].TaskID != "T-2" {
		t.Fatalf("taskId: %v", k)
	}
	if k := anRun([]AnomalyTask{expired("T-1"), expired("T-2")}, nil, func(in *DetectInput) { in.TaskID = "T-1" }); len(k) != 0 {
		t.Fatalf("taskId drops the board-level finding: %v", k)
	}
	sorted := anRun(tasks, nil)
	if sorted[0].Severity != SeverityCritical || sorted[0].TaskID != "T-1" || sorted[1].TaskID != "T-2" {
		t.Fatalf("critical first: %+v", sorted)
	}
	var full []AnomalyEvent
	for i := range 200 {
		full = append(full, anEv(EventTaskUpdated, "T-1", nil, time.Duration(i)*10*time.Millisecond))
	}
	_, scan := DetectAnomalies(DetectInput{Events: full, Now: anNow, Policy: DefaultAnomalyPolicy, EventLimit: 200})
	_, short := DetectAnomalies(DetectInput{Events: full[:5], Now: anNow, Policy: DefaultAnomalyPolicy, EventLimit: 200})
	if !scan.Truncated || scan.EventsScanned != 200 || short.Truncated {
		t.Fatalf("truncation: full %+v short %+v", scan, short)
	}
	thrash := append([]AnomalyEvent{anEv(EventTaskClaimed, "T-1", nil, 0), anEv(EventTaskClaimed, "T-1", nil, 0), anEv(EventTaskClaimed, "T-1", nil, 0)}, full[:197]...)
	if a := anRun([]AnomalyTask{anTask("T-1")}, thrash); len(a) != 1 || a[0].Evidence["windowTruncated"] != true {
		t.Fatalf("the truncation flag in the evidence: %+v", a)
	}
}

func TestEveryAnomalyKindIsReachable(t *testing.T) {
	shared := []AnomalyTask{
		anTask("T-2"), anTask("T-3"),
		anTask("T-6", func(t *AnomalyTask) { t.Priority, t.CreatedAt = PriorityUrgent, anAgo(9*24*time.Hour) }),
		anTask("T-7", func(t *AnomalyTask) { t.Status, t.BlockedBy = Blocked, []string{"T-8"} }),
		anTask("T-8", func(t *AnomalyTask) { t.Status, t.BlockedReason = Blocked, BlockedReasonPoison }),
	}
	events := []AnomalyEvent{anEv(EventTaskClaimed, "T-2", nil, time.Minute), anEv(EventTaskClaimed, "T-2", nil, time.Minute), anEv(EventTaskClaimed, "T-2", nil, time.Minute)}
	for range 2 {
		events = append(events, anEv(EventTaskMoved, "T-3", map[string]any{"from": "REVIEW", "to": "IN_PROGRESS"}, time.Minute))
	}
	seen := map[AnomalyKind]bool{}
	for _, a := range append(anRun(append(slices.Clone(shared), working("T-1", 5*time.Hour)), events), anRun(append(slices.Clone(shared), expired("T-4"), expired("T-5")), events)...) {
		seen[a.Kind] = true
	}
	for _, k := range AllAnomalyKinds() {
		if !seen[k] {
			t.Fatalf("%s is unreachable; seen %v", k, seen)
		}
	}
}

func TestFirstSightings(t *testing.T) {
	found := []Anomaly{{Kind: AnomalyClaimThrash, TaskID: "T-1"}, {Kind: AnomalyReworkLoop, TaskID: "T-1"}, {Kind: AnomalyClaimThrash, TaskID: "T-2"}, {Kind: AnomalyWIPJam}}
	if got := FirstSightings(found, nil); len(got) != 4 {
		t.Fatalf("no prior sighting: %v", got)
	}
	tail := []AnomalyEvent{
		anEv(EventTaskAnomalyDetected, "T-1", map[string]any{"kind": "claim_thrash"}, 0),
		anEv(EventTaskAnomalyDetected, "", map[string]any{"kind": "wip_jam"}, 0),
		anEv(EventTaskClaimed, "T-2", map[string]any{"kind": "claim_thrash"}, 0),
		{Type: EventTaskAnomalyDetected, TaskID: "T-2", Data: json.RawMessage(`[1]`)},
	}
	got := FirstSightings(found, tail)
	if len(got) != 2 || got[0].Kind != AnomalyReworkLoop || got[1].TaskID != "T-2" {
		t.Fatalf("after the tail: %+v, want rework_loop on T-1 and claim_thrash on T-2", got)
	}
}
