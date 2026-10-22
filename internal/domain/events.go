package domain

import (
	"fmt"
	"slices"
)

// EventType is the type of an event appended to a task's or a board's log. The vocabulary is closed and
// pinned here: an append with a type not listed is a bug, and ParseEventType refuses it. The names
// are conductor's, with two departures: task.poisoned replaces task.dead_lettered (plan §Fold 1),
// and task.deleted is new (delete_task, plan §Fold 2). task.requeued is gone with requeue_task:
// un-poisoning is a move_task, which appends task.moved.
type EventType string

const (
	EventAttachmentCreated         EventType = "attachment.created"
	EventAttachmentUpdated         EventType = "attachment.updated"
	EventBoardCreated              EventType = "board.created"
	EventChronicleConflictsFlagged EventType = "chronicle.conflicts_flagged"
	EventChroniclePageCreated      EventType = "chronicle.page_created"
	EventChroniclePageDeleted      EventType = "chronicle.page_deleted"
	EventChroniclePageUpdated      EventType = "chronicle.page_updated"
	EventChronicleVersionAdded     EventType = "chronicle.version_added"
	EventCommentAdded              EventType = "comment.added"
	EventContextAdded              EventType = "context.added"
	EventDependencyAdded           EventType = "dependency.added"
	EventDependencyRemoved         EventType = "dependency.removed"
	EventFollowupAcked             EventType = "followup.acked"
	EventIncidentDelivery          EventType = "incident.delivery"
	EventIncidentResolved          EventType = "incident.resolved"
	EventIntegrationCreated        EventType = "integration.created"
	EventIntegrationDeleted        EventType = "integration.deleted"
	EventIntegrationSecretRotated  EventType = "integration.secret_rotated"
	EventIntegrationUpdated        EventType = "integration.updated"
	EventMemoryWritten             EventType = "memory.written"
	EventProjectCreated            EventType = "project.created"
	EventRunDiscarded              EventType = "run.discarded"
	EventRunOpened                 EventType = "run.opened"
	EventRunPicked                 EventType = "run.picked"
	EventRunsCancelled             EventType = "runs.cancelled"
	EventRunsStarted               EventType = "runs.started"
	EventSprintAbandoned           EventType = "sprint.abandoned"
	EventSprintActivated           EventType = "sprint.activated"
	EventSprintCheckpoint          EventType = "sprint.checkpoint"
	EventSprintClosed              EventType = "sprint.closed"
	EventSprintCreated             EventType = "sprint.created"
	EventSprintSelfApproved        EventType = "sprint.self_approved"
	EventSprintTaskAdded           EventType = "sprint.task_added"
	EventSprintTaskRemoved         EventType = "sprint.task_removed"
	EventSprintUpdated             EventType = "sprint.updated"
	EventSummarySet                EventType = "summary.set"
	EventTaskAnomalyDetected       EventType = "task.anomaly_detected"
	EventTaskArtifactAdded         EventType = "task.artifact_added"
	EventTaskArtifactMissing       EventType = "task.artifact_missing"
	EventTaskArtifactRetracted     EventType = "task.artifact_retracted"
	EventTaskBlocked               EventType = "task.blocked"
	EventTaskCheckpointMissing     EventType = "task.checkpoint_missing"
	EventTaskClaimed               EventType = "task.claimed"
	EventTaskCompleted             EventType = "task.completed"
	EventTaskCreated               EventType = "task.created"
	EventTaskDeleted               EventType = "task.deleted"
	EventTaskDependencyCleared     EventType = "task.dependency_cleared"
	EventTaskFailed                EventType = "task.failed"
	EventTaskFollowUp              EventType = "task.follow_up"
	EventTaskHandoff               EventType = "task.handoff"
	EventTaskLeaseReaped           EventType = "task.lease_reaped"
	EventTaskMoved                 EventType = "task.moved"
	EventTaskOrchestrated          EventType = "task.orchestrated"
	EventTaskPoisoned              EventType = "task.poisoned"
	EventTaskReleased              EventType = "task.released"
	EventTaskReopened              EventType = "task.reopened"
	EventTaskReviewRequested       EventType = "task.review_requested"
	EventTaskSelfApproved          EventType = "task.self_approved"
	EventTaskUpdated               EventType = "task.updated"
)

var eventTypes = map[EventType]bool{
	EventAttachmentCreated:         true,
	EventAttachmentUpdated:         true,
	EventBoardCreated:              true,
	EventChronicleConflictsFlagged: true,
	EventChroniclePageCreated:      true,
	EventChroniclePageDeleted:      true,
	EventChroniclePageUpdated:      true,
	EventChronicleVersionAdded:     true,
	EventCommentAdded:              true,
	EventContextAdded:              true,
	EventDependencyAdded:           true,
	EventDependencyRemoved:         true,
	EventFollowupAcked:             true,
	EventIncidentDelivery:          true,
	EventIncidentResolved:          true,
	EventIntegrationCreated:        true,
	EventIntegrationDeleted:        true,
	EventIntegrationSecretRotated:  true,
	EventIntegrationUpdated:        true,
	EventMemoryWritten:             true,
	EventProjectCreated:            true,
	EventRunDiscarded:              true,
	EventRunOpened:                 true,
	EventRunPicked:                 true,
	EventRunsCancelled:             true,
	EventRunsStarted:               true,
	EventSprintAbandoned:           true,
	EventSprintActivated:           true,
	EventSprintCheckpoint:          true,
	EventSprintClosed:              true,
	EventSprintCreated:             true,
	EventSprintSelfApproved:        true,
	EventSprintTaskAdded:           true,
	EventSprintTaskRemoved:         true,
	EventSprintUpdated:             true,
	EventSummarySet:                true,
	EventTaskAnomalyDetected:       true,
	EventTaskArtifactAdded:         true,
	EventTaskArtifactMissing:       true,
	EventTaskArtifactRetracted:     true,
	EventTaskBlocked:               true,
	EventTaskCheckpointMissing:     true,
	EventTaskClaimed:               true,
	EventTaskCompleted:             true,
	EventTaskCreated:               true,
	EventTaskDeleted:               true,
	EventTaskDependencyCleared:     true,
	EventTaskFailed:                true,
	EventTaskFollowUp:              true,
	EventTaskHandoff:               true,
	EventTaskLeaseReaped:           true,
	EventTaskMoved:                 true,
	EventTaskOrchestrated:          true,
	EventTaskPoisoned:              true,
	EventTaskReleased:              true,
	EventTaskReopened:              true,
	EventTaskReviewRequested:       true,
	EventTaskSelfApproved:          true,
	EventTaskUpdated:               true,
}

// AllEventTypes returns the vocabulary, sorted.
func AllEventTypes() []EventType {
	out := make([]EventType, 0, len(eventTypes))
	for t := range eventTypes {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// IsEventType reports whether s is in the vocabulary.
func IsEventType(s string) bool {
	return eventTypes[EventType(s)]
}

// ParseEventType accepts exactly the vocabulary; anything else, a retired name included, is VALIDATION.
func ParseEventType(s string) (EventType, error) {
	if eventTypes[EventType(s)] {
		return EventType(s), nil
	}
	return "", Validation(fmt.Sprintf("unknown event type %q", s), map[string]any{"type": s})
}
