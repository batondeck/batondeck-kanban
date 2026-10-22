package domain

import (
	"reflect"
	"testing"
)

// The event vocabulary, pinned. Conductor has no such list (event types are string literals at each
// append); these names were read from every append in conductor src at 5e2baafa, minus the
// out-of-scope areas (plugin.*, org.*, registry.*, subscription.*).

func TestEventVocabularyPinned(t *testing.T) {
	want := []EventType{
		"attachment.created", "attachment.updated", "board.created",
		"chronicle.conflicts_flagged", "chronicle.page_created", "chronicle.page_deleted",
		"chronicle.page_updated", "chronicle.version_added", "comment.added", "context.added",
		"dependency.added", "dependency.removed", "followup.acked", "incident.delivery",
		"incident.resolved", "integration.created", "integration.deleted", "integration.secret_rotated",
		"integration.updated", "memory.written", "project.created", "run.discarded", "run.opened",
		"run.picked", "runs.cancelled", "runs.started", "sprint.abandoned", "sprint.activated",
		"sprint.checkpoint", "sprint.closed", "sprint.created", "sprint.self_approved",
		"sprint.task_added", "sprint.task_removed", "sprint.updated", "summary.set",
		"task.anomaly_detected", "task.artifact_added", "task.artifact_missing",
		"task.artifact_retracted", "task.blocked", "task.checkpoint_missing", "task.claimed",
		"task.completed", "task.created", "task.deleted", "task.dependency_cleared", "task.failed",
		"task.follow_up", "task.handoff", "task.lease_reaped", "task.moved", "task.orchestrated",
		"task.poisoned", "task.released", "task.reopened", "task.review_requested",
		"task.self_approved", "task.updated",
	}
	if got := AllEventTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("event vocabulary\n got %q\nwant %q", got, want)
	}
}

// AC5.
func TestEventTypeRefusesRetiredAndUnknown(t *testing.T) {
	for _, s := range []string{"task.dead_lettered", "task.requeued", "task.cancelled", "plugin.created", "task.unknown", ""} {
		_, err := ParseEventType(s)
		if e, ok := AsBatonDeckError(err); !ok || e.Code != CodeValidation || IsEventType(s) {
			t.Errorf("ParseEventType(%q) = %v, want VALIDATION", s, err)
		}
	}
	for _, s := range []string{"task.poisoned", "task.deleted", "run.discarded"} {
		if got, err := ParseEventType(s); err != nil || string(got) != s || !IsEventType(s) {
			t.Errorf("ParseEventType(%q) = %q, %v, want accepted", s, got, err)
		}
	}
}
