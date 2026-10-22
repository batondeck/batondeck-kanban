package domain

import "fmt"

// Column is a board column: the status it maps to and its WIP limit (nil: no limit).
type Column struct {
	ID       string
	Name     string
	Order    int
	Status   Status
	WIPLimit *int
}

// Board is the part of a board the move decision reads.
type Board struct {
	ID        string
	ProjectID string
	Name      string
	Columns   []Column
}

// MoveTarget is where a move lands: the column, which fixes the status.
type MoveTarget struct {
	ColumnID string
	Status   Status
}

// ResolveColumnAndStatus turns a move's target into a column and a status. A target column fixes the
// status; a target status alone resolves to the one column mapping it, or, when no column maps it, to
// currentColumnID (the status changes in place). "" means absent for each argument.
func ResolveColumnAndStatus(board Board, toColumnID string, toStatus Status, currentColumnID string) (MoveTarget, error) {
	if toColumnID != "" {
		for _, c := range board.Columns {
			if c.ID != toColumnID {
				continue
			}
			if toStatus != "" && toStatus != c.Status {
				return MoveTarget{}, Validation(fmt.Sprintf("toStatus %s conflicts with column %s status %s", toStatus, c.ID, c.Status), nil)
			}
			return MoveTarget{ColumnID: c.ID, Status: c.Status}, nil
		}
		return MoveTarget{}, NotFound("Column", toColumnID)
	}
	if toStatus != "" {
		var match *Column
		n := 0
		for i := range board.Columns {
			if board.Columns[i].Status == toStatus {
				n++
				match = &board.Columns[i]
			}
		}
		switch {
		case n == 0 && currentColumnID != "":
			return MoveTarget{ColumnID: currentColumnID, Status: toStatus}, nil
		case n == 0:
			return MoveTarget{}, Validation(fmt.Sprintf("No column maps to status %s", toStatus), nil)
		case n > 1:
			return MoveTarget{}, Validation(fmt.Sprintf("Ambiguous: %d columns map to %s", n, toStatus), nil)
		}
		return MoveTarget{ColumnID: match.ID, Status: toStatus}, nil
	}
	return MoveTarget{}, Validation("move_task requires toColumnId or toStatus", nil)
}

// MoveRequest is everything the move decision reads, taken from one snapshot of the task.
type MoveRequest struct {
	Board        Board
	FromStatus   Status
	FromColumnID string
	// OpenBlockers is the length of the task's blockedBy.
	OpenBlockers int
	ToColumnID   string
	ToStatus     Status
	// Order is the caller's order key, "" for none.
	Order string
	// InProgressCount is the number of the board's tasks in IN_PROGRESS, counted inside the same
	// transaction as the write so two concurrent moves cannot both take the last slot.
	InProgressCount int
}

// MoveDecision is where a legal move lands.
type MoveDecision struct {
	ColumnID string
	Status   Status
	// RedirectedToBlocked: the task was promoted with open blockers and lands on BLOCKED instead.
	RedirectedToBlocked bool
	// Order is the caller's key; "" keeps the task's order, unless NeedsEndOfColumnOrder.
	Order string
	// NeedsEndOfColumnOrder: the task changes column with no key given, so it goes after the
	// landing column's last key (OrderBetween(last, "")).
	NeedsEndOfColumnOrder bool
}

// DecideMove applies the move rules: the fixed transition table, a promotion of a still-blocked
// task landing on BLOCKED, and the WIP limit, which applies only to entering IN_PROGRESS. A move to
// the same status is a reorder.
func DecideMove(req MoveRequest) (MoveDecision, error) {
	if req.Order != "" && !IsValidOrder(req.Order) {
		return MoveDecision{}, Validation(fmt.Sprintf("Invalid order key: %q", req.Order), nil)
	}
	if req.ToStatus != "" {
		if _, err := ParseStatus(string(req.ToStatus)); err != nil {
			return MoveDecision{}, err
		}
	}
	target, err := ResolveColumnAndStatus(req.Board, req.ToColumnID, req.ToStatus, req.FromColumnID)
	if err != nil {
		return MoveDecision{}, err
	}
	if !CanTransition(req.FromStatus, target.Status) {
		return MoveDecision{}, InvalidTransition(req.FromStatus, target.Status,
			AllowedNext(req.FromStatus), ShortestPath(req.FromStatus, target.Status))
	}

	// The fixed table always gives BLOCKED a way out, so a board models blocked when it has a
	// BLOCKED column. A same-status move is a reorder and is never redirected.
	blockedColumnID := ""
	for _, c := range req.Board.Columns {
		if c.Status == Blocked {
			blockedColumnID = c.ID
			break
		}
	}
	wantsWork := target.Status == Ready || target.Status == InProgress
	d := MoveDecision{ColumnID: target.ColumnID, Status: target.Status, Order: req.Order}
	if blockedColumnID != "" && wantsWork && req.FromStatus != target.Status && req.OpenBlockers > 0 {
		d.ColumnID, d.Status, d.RedirectedToBlocked = blockedColumnID, Blocked, true
	}

	// The limit is read from the requested column and keyed on where the task lands, so a redirect
	// to BLOCKED neither takes nor is refused an IN_PROGRESS slot.
	if d.Status == InProgress && req.FromStatus != InProgress {
		for _, c := range req.Board.Columns {
			if c.ID == target.ColumnID && c.WIPLimit != nil && req.InProgressCount >= *c.WIPLimit {
				return MoveDecision{}, WIPExceeded(d.ColumnID, *c.WIPLimit)
			}
		}
	}

	d.NeedsEndOfColumnOrder = req.Order == "" && d.ColumnID != req.FromColumnID
	return d, nil
}
