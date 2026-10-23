package domain

import (
	"reflect"
	"testing"
)

func edges(pairs ...[2]string) []Edge {
	out := make([]Edge, len(pairs))
	for i, p := range pairs {
		out[i] = Edge{FromTaskID: p[0], ToTaskID: p[1]}
	}
	return out
}

// cycles runs the production path, CheckCycle, and requires a refusal to be CYCLE_DETECTED.
func cycles(t *testing.T, g []Edge, from, to string) bool {
	t.Helper()
	err := CheckCycle(g, from, to)
	if err == nil {
		return false
	}
	if e, ok := AsBatonDeckError(err); !ok || e.Code != CodeCycleDetected {
		t.Fatalf("CheckCycle(%s, %s) = %v, want CYCLE_DETECTED", from, to, err)
	}
	return true
}

func TestWouldCreateCycle(t *testing.T) {
	t.Run("rejects a self-edge, even on an empty graph", func(t *testing.T) {
		if !cycles(t, nil, "T-1", "T-1") || !cycles(t, edges([2]string{"A", "B"}), "A", "A") {
			t.Fatal("self-edge accepted")
		}
	})
	t.Run("is DIRECTIONAL: A→B exists ⇒ B→A closes a cycle, a second A→B does not", func(t *testing.T) {
		g := edges([2]string{"A", "B"})
		if !cycles(t, g, "B", "A") || cycles(t, g, "A", "B") {
			t.Fatal("direction ignored")
		}
	})
	t.Run("walks a long chain: A→B→C→D→E ⇒ only an edge back into the chain cycles", func(t *testing.T) {
		chain := edges([2]string{"A", "B"}, [2]string{"B", "C"}, [2]string{"C", "D"}, [2]string{"D", "E"})
		if !cycles(t, chain, "E", "A") || !cycles(t, chain, "E", "C") || cycles(t, chain, "A", "E") || !cycles(t, chain, "C", "A") {
			t.Fatal("chain verdicts wrong")
		}
	})
	t.Run("a diamond (two paths converging) is NOT a cycle, but an edge back from its sink is", func(t *testing.T) {
		d := edges([2]string{"A", "B"}, [2]string{"A", "C"}, [2]string{"B", "D"}, [2]string{"C", "D"})
		if cycles(t, d, "B", "C") || !cycles(t, d, "D", "A") || !cycles(t, d, "D", "B") || cycles(t, d, "A", "D") {
			t.Fatal("diamond verdicts wrong")
		}
	})
	t.Run("never crosses into a disconnected component", func(t *testing.T) {
		two := edges([2]string{"A", "B"}, [2]string{"X", "Y"})
		if cycles(t, two, "A", "Y") || cycles(t, two, "Y", "A") {
			t.Fatal("crossed components")
		}
	})
	t.Run("follows EVERY out-edge of a fan-out node, not just the last one", func(t *testing.T) {
		fan := edges([2]string{"A", "B"}, [2]string{"A", "B"}, [2]string{"A", "DEAD"}, [2]string{"B", "C"})
		if !cycles(t, fan, "C", "A") {
			t.Fatal("missed the cycle through A's first edge")
		}
	})
	t.Run("terminates on a graph that already contains a cycle", func(t *testing.T) {
		cyclic := edges([2]string{"A", "B"}, [2]string{"B", "A"})
		if cycles(t, cyclic, "C", "A") || !cycles(t, cyclic, "A", "B") {
			t.Fatal("cyclic-graph verdicts wrong")
		}
	})
}

// TestPlantedCycle: with A→B in place, adding B→A is refused CYCLE_DETECTED naming the new edge.
func TestPlantedCycle(t *testing.T) {
	err := CheckCycle(edges([2]string{"A", "B"}), "B", "A")
	e, ok := AsBatonDeckError(err)
	if !ok || e.Code != CodeCycleDetected || !reflect.DeepEqual(e.Details, map[string]any{"fromTaskId": "B", "toTaskId": "A"}) {
		t.Fatalf("CheckCycle = %v", err)
	}
	if err := CheckCycle(edges([2]string{"A", "B"}), "B", "C"); err != nil {
		t.Fatalf("B→C refused: %v", err)
	}
}
