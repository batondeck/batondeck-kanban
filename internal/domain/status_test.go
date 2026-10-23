package domain

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// legalMoves is ADR-0005's table, written out here independently of the source so
// that removing an entry from fixedTransitions fails TestTransitionTable.
var legalMoves = map[[2]Status]bool{
	{Todo, InProgress}:       true,
	{InProgress, Review}:     true,
	{InProgress, Blocked}:    true,
	{InProgress, Todo}:       true,
	{Blocked, InProgress}:    true,
	{Blocked, Todo}:          true,
	{Review, Done}:           true,
	{Review, InProgress}:     true,
	{Done, InProgress}:       true,
	{Todo, Todo}:             true,
	{InProgress, InProgress}: true,
	{Blocked, Blocked}:       true,
	{Review, Review}:         true,
	{Done, Done}:             true,
}

// TestTransitionTable is the 5×5 proof, conductor's "canTransition matches the default table exactly
// (incl. no-op self moves)" with the expected verdicts written out rather than read from the table.
func TestTransitionTable(t *testing.T) {
	if n, legal := len(AllStatuses()), len(legalMoves); n != 5 || legal != 14 {
		t.Fatalf("%d statuses and %d legal pairs, ADR-0005 says 5 and 14 of 25", n, legal)
	}
	for _, from := range AllStatuses() {
		for _, to := range AllStatuses() {
			want := legalMoves[[2]Status{from, to}]
			t.Run(fmt.Sprintf("%s->%s", from, to), func(t *testing.T) {
				if got := CanTransition(from, to); got != want {
					t.Fatalf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
				}
			})
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range []string{"TODO", "IN_PROGRESS", "BLOCKED", "REVIEW", "DONE"} {
		t.Run("accepts "+s, func(t *testing.T) {
			got, err := ParseStatus(s)
			if err != nil || got != Status(s) {
				t.Fatalf("ParseStatus(%q) = %q, %v", s, got, err)
			}
		})
	}
	for _, s := range []string{"CANCELLED", "DEAD_LETTER", "", "todo", "BACK" + "LOG", "RE" + "ADY"} {
		t.Run("refuses "+s, func(t *testing.T) {
			_, err := ParseStatus(s)
			e, ok := AsBatonDeckError(err)
			if !ok || e.Code != CodeValidation {
				t.Fatalf("ParseStatus(%q) error = %v, want VALIDATION", s, err)
			}
		})
	}
}

func TestStatusStateMachine(t *testing.T) {
	t.Run("DEAD_LETTER is server-only — no manual transition into or out of it", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). DEAD_LETTER does not exist: poison is BLOCKED.
		for _, s := range AllStatuses() {
			if CanTransition(s, "DEAD_LETTER") || CanTransition("DEAD_LETTER", s) {
				t.Fatalf("a move between %s and DEAD_LETTER is legal", s)
			}
		}
	})
	t.Run("CANCELLED is terminal", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). CANCELLED does not exist: it is a hard delete,
		// so no move reaches it or leaves it.
		for _, s := range AllStatuses() {
			if CanTransition(s, "CANCELLED") || CanTransition("CANCELLED", s) {
				t.Fatalf("a move between %s and CANCELLED is legal", s)
			}
		}
		if CanTransition("CANCELLED", "CANCELLED") {
			t.Fatal("CANCELLED->CANCELLED is legal")
		}
	})
	t.Run("honors specific documented edges", func(t *testing.T) {
		for _, c := range []struct {
			from, to Status
			want     bool
		}{
			{Todo, InProgress, true},
			{InProgress, Review, true},
			{Review, Done, true},
			{Done, InProgress, true},
			{Todo, Done, false},
			{Todo, Review, false},
			{Todo, Blocked, false},
		} {
			if got := CanTransition(c.from, c.to); got != c.want {
				t.Fatalf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
			}
		}
	})
	t.Run("supports per-project table overrides", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). The override is removed: there is no table
		// parameter, so an override edge TODO->DONE is illegal and TODO->IN_PROGRESS legal.
		if CanTransition(Todo, Done) {
			t.Fatal("TODO->DONE is legal")
		}
		if !CanTransition(Todo, InProgress) {
			t.Fatal("TODO->IN_PROGRESS is illegal")
		}
	})
}

func TestTransitionGuidance(t *testing.T) {
	t.Run("allowedNext lists the legal one-step targets", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). TODO has no CANCELLED target, and CANCELLED
		// is not a status, so it has no targets at all.
		if got := AllowedNext(Todo); !reflect.DeepEqual(got, []Status{InProgress}) {
			t.Fatalf("AllowedNext(TODO) = %v", got)
		}
		if got := AllowedNext(Review); !reflect.DeepEqual(got, []Status{Done, InProgress}) {
			t.Fatalf("AllowedNext(REVIEW) = %v", got)
		}
		if got := AllowedNext("CANCELLED"); got != nil {
			t.Fatalf("AllowedNext(CANCELLED) = %v", got)
		}
	})
	t.Run("transitionPath finds the shortest legal route (TODO→DONE ⇒ full flow)", func(t *testing.T) {
		for _, c := range []struct {
			from, to Status
			want     []Status
		}{
			{Todo, Done, []Status{Todo, InProgress, Review, Done}},
			{Todo, Todo, []Status{Todo}},
			{Todo, Blocked, []Status{Todo, InProgress, Blocked}},
		} {
			if got := ShortestPath(c.from, c.to); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ShortestPath(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
			}
		}
	})
	t.Run("transitionPath returns null when the target is unreachable", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). The closed table reaches every status from every
		// status, so only a name that is not a status has no path.
		if got := ShortestPath("CANCELLED", Done); got != nil {
			t.Fatalf("ShortestPath(CANCELLED, DONE) = %v", got)
		}
		if got := ShortestPath(Done, "DEAD_LETTER"); got != nil {
			t.Fatalf("ShortestPath(DONE, DEAD_LETTER) = %v", got)
		}
		for _, from := range AllStatuses() {
			for _, to := range AllStatuses() {
				if ShortestPath(from, to) == nil {
					t.Fatalf("no path %s -> %s", from, to)
				}
			}
		}
	})
	t.Run("invalidTransition error message guides the agent to the real route", func(t *testing.T) {
		// DEVIATION: five states (ADR-0005). The allowed list from TODO is IN_PROGRESS alone.
		err := InvalidTransition(Todo, Done, AllowedNext(Todo), ShortestPath(Todo, Done))
		if err.Code != CodeInvalidTransition {
			t.Fatalf("code = %s", err.Code)
		}
		for _, want := range []string{"From TODO you can move to: IN_PROGRESS.", "TODO → IN_PROGRESS → REVIEW → DONE"} {
			if !strings.Contains(err.Message, want) {
				t.Fatalf("message %q lacks %q", err.Message, want)
			}
		}
		if err.Details["from"] != Todo || err.Details["to"] != Done || !reflect.DeepEqual(err.Details["allowed"], []Status{InProgress}) {
			t.Fatalf("details = %v", err.Details)
		}
	})
	t.Run(`WITHOUT a guide, invalidTransition never fabricates a "no outgoing moves" claim`, func(t *testing.T) {
		err := InvalidTransition(Review, InProgress, nil, nil)
		if err.Message != "Illegal status transition REVIEW → IN_PROGRESS." {
			t.Fatalf("message = %q", err.Message)
		}
		if !reflect.DeepEqual(err.Details, map[string]any{"from": Review, "to": InProgress}) {
			t.Fatalf("details = %v", err.Details)
		}
		// A path without an allowed list names the route and claims no moves.
		err = InvalidTransition(Review, Todo, nil, ShortestPath(Review, Todo))
		if strings.Contains(err.Message, "you can move to") || !strings.Contains(err.Message, "REVIEW → IN_PROGRESS → TODO") {
			t.Fatalf("message = %q", err.Message)
		}
	})
}

// TestInvalidTransitionReviewToTodo: the guidance names REVIEW's two moves and the shortest route
// back to TODO.
func TestInvalidTransitionReviewToTodo(t *testing.T) {
	if CanTransition(Review, Todo) {
		t.Fatal("REVIEW->TODO is legal")
	}
	err := InvalidTransition(Review, Todo, AllowedNext(Review), ShortestPath(Review, Todo))
	if got := err.Details["allowed"]; !reflect.DeepEqual(got, []Status{Done, InProgress}) {
		t.Fatalf("allowed = %v", got)
	}
	if got := err.Details["path"]; !reflect.DeepEqual(got, []Status{Review, InProgress, Todo}) {
		t.Fatalf("path = %v", got)
	}
	if !strings.Contains(err.Message, "REVIEW → IN_PROGRESS → TODO") {
		t.Fatalf("message = %q", err.Message)
	}
}
