package domain

// AutoReady is the auto-unblock guard: a dependent moves BLOCKED → TODO when its last blocker
// clears, unless it is poisoned. A poisoned task stays BLOCKED whatever its dependencies do; only a
// master's un-poison releases it (plan §Fold 1). Every auto-unblock site calls this.
func AutoReady(blockedBy []string, status Status, blockedReason string) bool {
	return len(blockedBy) == 0 && status == Blocked && blockedReason != BlockedReasonPoison
}

// UnblockDecision is what clearing one blocker from a dependent writes.
type UnblockDecision struct {
	// Cleared is false when clearedID was not among the dependent's blockers: nothing is written.
	Cleared bool
	// BlockedBy is the dependent's blockers without clearedID.
	BlockedBy []string
	// AutoReady: the dependent moves to TODO in the same transaction.
	AutoReady bool
}

// DecideUnblock clears clearedID (a blocker that reached DONE or was deleted) from a dependent's
// blockedBy and applies the guard. The caller appends EventTaskDependencyCleared with
// {clearedBy, autoReady} when Cleared.
func DecideUnblock(blockedBy []string, status Status, blockedReason, clearedID string) UnblockDecision {
	d := UnblockDecision{BlockedBy: make([]string, 0, len(blockedBy))}
	for _, id := range blockedBy {
		if id == clearedID {
			d.Cleared = true
			continue
		}
		d.BlockedBy = append(d.BlockedBy, id)
	}
	if !d.Cleared {
		d.BlockedBy = append([]string(nil), blockedBy...)
		return d
	}
	d.AutoReady = AutoReady(d.BlockedBy, status, blockedReason)
	return d
}
