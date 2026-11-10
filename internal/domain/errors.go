package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Code is a stable error code. Codes are public API: a client branches on them.
type Code string

const (
	CodeValidation        Code = "VALIDATION"
	CodeUnauthenticated   Code = "UNAUTHENTICATED"
	CodeForbidden         Code = "FORBIDDEN"
	CodeNotFound          Code = "NOT_FOUND"
	CodeStale             Code = "STALE"
	CodeConflictLocked    Code = "CONFLICT_LOCKED"
	CodeLeaseExpired      Code = "LEASE_EXPIRED"
	CodeInvalidTransition Code = "INVALID_TRANSITION"
	CodeWIPExceeded       Code = "WIP_EXCEEDED"
	CodeCycleDetected     Code = "CYCLE_DETECTED"
	CodeQuotaExceeded     Code = "QUOTA_EXCEEDED"
	CodeRateLimited       Code = "RATE_LIMITED"
	CodeInternal          Code = "INTERNAL"
)

// AllCodes returns every error code; errors_test.go pins the list.
func AllCodes() []Code {
	return []Code{
		CodeValidation, CodeUnauthenticated, CodeForbidden, CodeNotFound, CodeStale,
		CodeConflictLocked, CodeLeaseExpired, CodeInvalidTransition, CodeWIPExceeded,
		CodeCycleDetected, CodeQuotaExceeded, CodeRateLimited, CodeInternal,
	}
}

// IsRetryable reports whether a client may retry a call that failed with code.
func IsRetryable(code Code) bool {
	return code == CodeStale || code == CodeRateLimited || code == CodeInternal
}

// BatonDeckError is the one error shape every tool returns. Its JSON form is the structured payload
// a client receives: code, message, retryable, and details when there are any.
type BatonDeckError struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable"`
}

func (e *BatonDeckError) Error() string { return string(e.Code) + ": " + e.Message }

// NewError builds a BatonDeckError; Retryable follows from the code.
func NewError(code Code, message string, details map[string]any) *BatonDeckError {
	return &BatonDeckError{Code: code, Message: message, Details: details, Retryable: IsRetryable(code)}
}

// AsBatonDeckError finds a BatonDeckError in err's chain.
func AsBatonDeckError(err error) (*BatonDeckError, bool) {
	var e *BatonDeckError
	ok := errors.As(err, &e)
	return e, ok
}

func Validation(message string, details map[string]any) *BatonDeckError {
	return NewError(CodeValidation, message, details)
}

func NotFound(entity, id string) *BatonDeckError {
	if id == "" {
		return NewError(CodeNotFound, entity+" not found", map[string]any{"entity": entity})
	}
	return NewError(CodeNotFound, entity+" not found: "+id, map[string]any{"entity": entity, "id": id})
}

func Stale(expected, actual int64) *BatonDeckError {
	return NewError(CodeStale, fmt.Sprintf("Version mismatch: expected %d, found %d", expected, actual),
		map[string]any{"expected": expected, "actual": actual})
}

func ConflictLocked(taskID, owner string) *BatonDeckError {
	return NewError(CodeConflictLocked, fmt.Sprintf("Task %s is claimed by %s", taskID, owner),
		map[string]any{"taskId": taskID, "owner": owner})
}

// InvalidTransition reports an illegal from→to move. With allowed and path both nil it is the bare
// message (a lifecycle path that has no guidance to give); otherwise it names the legal next statuses
// and, when path has more than one step, the shortest legal route.
func InvalidTransition(from, to Status, allowed, path []Status) *BatonDeckError {
	message := fmt.Sprintf("Illegal status transition %s → %s.", from, to)
	details := map[string]any{"from": from, "to": to}
	if allowed != nil || path != nil {
		if allowed == nil {
			allowed = []Status{}
		}
		details["allowed"] = allowed
		if len(allowed) > 0 {
			message += fmt.Sprintf(" From %s you can move to: %s.", from, joinStatuses(allowed, ", "))
		}
		if len(path) > 1 {
			details["path"] = path
			message += fmt.Sprintf(" To reach %s, go step by step: %s.", to, joinStatuses(path, " → "))
		}
	}
	return NewError(CodeInvalidTransition, message, details)
}

// GatedClaim is the INVALID_TRANSITION of a claim of a task that still has open blockers: its move
// to IN_PROGRESS waits on them, and the message and details name them (blockedBy), so the caller
// walks the chain rather than reading the one legal move out of TODO as illegal.
func GatedClaim(taskID string, from Status, blockedBy []string) *BatonDeckError {
	return NewError(CodeInvalidTransition,
		fmt.Sprintf("Task %s is blocked by %s: it can be claimed once they are DONE.", taskID, strings.Join(blockedBy, ", ")),
		map[string]any{"from": from, "to": InProgress, "taskId": taskID, "blockedBy": blockedBy})
}

// InBackoff is the INVALID_TRANSITION of a claim of a TODO task still inside its retry backoff
// (IsClaimable: past nextEligibleAt), which fail_task and the reaper set: the message and details
// name when it can be claimed (nextEligibleAt, RFC 3339 in UTC with milliseconds).
func InBackoff(taskID string, nextEligibleAt time.Time) *BatonDeckError {
	at := nextEligibleAt.UTC().Format("2006-01-02T15:04:05.000Z")
	return NewError(CodeInvalidTransition, fmt.Sprintf("Task %s is in its retry backoff until %s: it can be claimed then.", taskID, at),
		map[string]any{"from": Todo, "to": InProgress, "taskId": taskID, "nextEligibleAt": at})
}

func WIPExceeded(columnID string, limit int) *BatonDeckError {
	return NewError(CodeWIPExceeded, fmt.Sprintf("Column %s WIP limit %d reached", columnID, limit),
		map[string]any{"columnId": columnID, "limit": limit})
}

func CycleDetected(fromTaskID, toTaskID string) *BatonDeckError {
	return NewError(CodeCycleDetected, fmt.Sprintf("Dependency %s → %s would create a cycle", fromTaskID, toTaskID),
		map[string]any{"fromTaskId": fromTaskID, "toTaskId": toTaskID})
}

func joinStatuses(ss []Status, sep string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = string(s)
	}
	return strings.Join(parts, sep)
}
