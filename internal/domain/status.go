// Package domain holds BatonDeck's pure rules: the status state machine, the move decision, the
// error model, order keys and dependency cycles. Nothing here touches a store or the clock.
package domain

import "fmt"

// Status is a ticket's workflow state. There are six; CANCELLED and DEAD_LETTER do not exist here
// (plan §Ticket states: CANCELLED is a hard delete, DEAD_LETTER is BLOCKED with reason "poison").
type Status string

const (
	Backlog    Status = "BACKLOG"
	Ready      Status = "READY"
	InProgress Status = "IN_PROGRESS"
	Blocked    Status = "BLOCKED"
	Review     Status = "REVIEW"
	Done       Status = "DONE"
)

// AllStatuses returns the six statuses in workflow order.
func AllStatuses() []Status {
	return []Status{Backlog, Ready, InProgress, Blocked, Review, Done}
}

// fixedTransitions is the transition table: closed (every status has an entry) and not editable.
// The order of each list is the order guidance reports and the tie-break ShortestPath takes.
var fixedTransitions = map[Status][]Status{
	Backlog:    {Ready},
	Ready:      {InProgress, Backlog},
	InProgress: {Review, Blocked, Ready},
	Blocked:    {InProgress, Ready},
	Review:     {Done, InProgress},
	Done:       {InProgress},
}

// ParseStatus accepts exactly the six statuses; anything else is VALIDATION.
func ParseStatus(s string) (Status, error) {
	if _, ok := fixedTransitions[Status(s)]; ok {
		return Status(s), nil
	}
	return "", Validation(fmt.Sprintf("unknown status %q", s), map[string]any{"status": s, "allowed": AllStatuses()})
}

// CanTransition reports whether from→to is a legal move. A move to the same status is always legal
// (a reorder within a column).
func CanTransition(from, to Status) bool {
	if _, ok := fixedTransitions[from]; !ok {
		return false
	}
	if from == to {
		return true
	}
	for _, s := range fixedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// AllowedNext returns the statuses reachable in one legal move from from, or nil for an unknown status.
func AllowedNext(from Status) []Status {
	next, ok := fixedTransitions[from]
	if !ok {
		return nil
	}
	return append([]Status(nil), next...)
}

// ShortestPath returns the shortest legal path from→to, both ends included, or nil when either end
// is not a status. Breadth-first over the table, so it guides an INVALID_TRANSITION to the real route.
func ShortestPath(from, to Status) []Status {
	if _, ok := fixedTransitions[from]; !ok {
		return nil
	}
	if _, ok := fixedTransitions[to]; !ok {
		return nil
	}
	if from == to {
		return []Status{from}
	}
	queue := [][]Status{{from}}
	seen := map[Status]bool{from: true}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		for _, next := range fixedTransitions[path[len(path)-1]] {
			if seen[next] {
				continue
			}
			extended := append(append([]Status(nil), path...), next)
			if next == to {
				return extended
			}
			seen[next] = true
			queue = append(queue, extended)
		}
	}
	return nil
}
