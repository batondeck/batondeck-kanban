package domain

// Edge is a "blocks" dependency inside one project: From blocks To (To is blocked by From).
type Edge struct {
	FromTaskID string
	ToTaskID   string
}

// CheckCycle returns CYCLE_DETECTED when adding from→to to edges would close a cycle, else nil.
// The edges must all belong to the project the two task ids are in (ids are per-project).
func CheckCycle(edges []Edge, from, to string) error {
	if wouldCreateCycle(edges, from, to) {
		return CycleDetected(from, to)
	}
	return nil
}

// wouldCreateCycle: from→to closes a cycle iff to already reaches from along the edges.
func wouldCreateCycle(edges []Edge, from, to string) bool {
	if from == to {
		return true
	}
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.FromTaskID] = append(adj[e.FromTaskID], e.ToTaskID)
	}
	stack := []string{to}
	seen := map[string]bool{}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == from {
			return true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, adj[n]...)
	}
	return false
}
