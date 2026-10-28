// Package domain holds BatonDeck's pure rules: the status state machine, the move decision, the
// error model, order keys, dependency cycles, claimability, retry and poison, the auto-unblock guard,
// roles and capability grants, audit descriptors, the event vocabulary and the system labels.
// Nothing here touches a store or reads the clock: the time is an argument.
package domain

import "fmt"

// Status is a ticket's workflow state. There are five (ADR-0005: no created task is a
// draft, so every one lands TODO); CANCELLED and DEAD_LETTER do not exist here (ADR-0001:
// cancel_task moves a task to DONE with the system label "cancelled" and delete_task is the hard
// delete; DEAD_LETTER is BLOCKED with reason "poison" and the system label "poisoned").
type Status string

const (
	Todo       Status = "TODO"
	InProgress Status = "IN_PROGRESS"
	Blocked    Status = "BLOCKED"
	Review     Status = "REVIEW"
	Done       Status = "DONE"
)

// AllStatuses returns the five statuses in workflow order.
func AllStatuses() []Status {
	return []Status{Todo, InProgress, Blocked, Review, Done}
}

// fixedTransitions is the transition table: closed (every status has an entry) and not editable.
// The order of each list is the order guidance reports and the tie-break ShortestPath takes.
var fixedTransitions = map[Status][]Status{
	Todo:       {InProgress},
	InProgress: {Review, Blocked, Todo},
	Blocked:    {InProgress, Todo},
	Review:     {Done, InProgress},
	Done:       {InProgress},
}

// ParseStatus accepts exactly the five statuses; anything else is VALIDATION.
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
