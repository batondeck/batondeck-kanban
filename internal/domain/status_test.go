package domain

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// legalMoves is the plan's table (§Ticket states), written out here independently of the source so
// that removing an entry from fixedTransitions fails TestTransitionTable.
var legalMoves = map[[2]Status]bool{
	{Backlog, Ready}:         true,
	{Ready, InProgress}:      true,
	{Ready, Backlog}:         true,
	{InProgress, Review}:     true,
	{InProgress, Blocked}:    true,
	{InProgress, Ready}:      true,
	{Blocked, InProgress}:    true,
	{Blocked, Ready}:         true,
	{Review, Done}:           true,
	{Review, InProgress}:     true,
	{Done, InProgress}:       true,
	{Backlog, Backlog}:       true,
	{Ready, Ready}:           true,
	{InProgress, InProgress}: true,
	{Blocked, Blocked}:       true,
	{Review, Review}:         true,
	{Done, Done}:             true,
}

// TestTransitionTable is the 6×6 proof, conductor's "canTransition matches the default table exactly
// (incl. no-op self moves)" with the expected verdicts written out rather than read from the table.
func TestTransitionTable(t *testing.T) {
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
	for _, s := range []string{"BACKLOG", "READY", "IN_PROGRESS", "BLOCKED", "REVIEW", "DONE"} {
		t.Run("accepts "+s, func(t *testing.T) {
			got, err := ParseStatus(s)
			if err != nil || got != Status(s) {
				t.Fatalf("ParseStatus(%q) = %q, %v", s, got, err)
			}
		})
	}
	for _, s := range []string{"CANCELLED", "DEAD_LETTER", "", "backlog"} {
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
		// DEVIATION: six states (plan §Ticket states). DEAD_LETTER does not exist: poison is BLOCKED.
		for _, s := range AllStatuses() {
			if CanTransition(s, "DEAD_LETTER") || CanTransition("DEAD_LETTER", s) {
				t.Fatalf("a move between %s and DEAD_LETTER is legal", s)
			}
		}
	})
	t.Run("CANCELLED is terminal", func(t *testing.T) {
		// DEVIATION: six states (plan §Ticket states). CANCELLED does not exist: it is a hard delete,
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
			{Ready, InProgress, true},
			{InProgress, Review, true},
			{Review, Done, true},
			{Done, InProgress, true},
			{Backlog, Done, false},
			{Backlog, InProgress, false},
		} {
			if got := CanTransition(c.from, c.to); got != c.want {
				t.Fatalf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
			}
		}
	})
	t.Run("supports per-project table overrides", func(t *testing.T) {
		// DEVIATION: six states (plan §Ticket states). The override is removed: there is no table
		// parameter, so conductor's override edge BACKLOG->DONE is illegal and BACKLOG->READY legal.
		if CanTransition(Backlog, Done) {
			t.Fatal("BACKLOG->DONE is legal")
		}
		if !CanTransition(Backlog, Ready) {
			t.Fatal("BACKLOG->READY is illegal")
		}
	})
}

func TestTransitionGuidance(t *testing.T) {
	t.Run("allowedNext lists the legal one-step targets", func(t *testing.T) {
		// DEVIATION: six states (plan §Ticket states). BACKLOG has no CANCELLED target, and CANCELLED
		// is not a status, so it has no targets at all.
		if got := AllowedNext(Backlog); !reflect.DeepEqual(got, []Status{Ready}) {
			t.Fatalf("AllowedNext(BACKLOG) = %v", got)
		}
		if got := AllowedNext(Review); !reflect.DeepEqual(got, []Status{Done, InProgress}) {
			t.Fatalf("AllowedNext(REVIEW) = %v", got)
		}
		if got := AllowedNext("CANCELLED"); got != nil {
			t.Fatalf("AllowedNext(CANCELLED) = %v", got)
		}
	})
	t.Run("transitionPath finds the shortest legal route (BACKLOG→DONE ⇒ full flow)", func(t *testing.T) {
		for _, c := range []struct {
			from, to Status
			want     []Status
		}{
			{Backlog, Done, []Status{Backlog, Ready, InProgress, Review, Done}},
			{Ready, Ready, []Status{Ready}},
			{Backlog, InProgress, []Status{Backlog, Ready, InProgress}},
		} {
			if got := ShortestPath(c.from, c.to); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ShortestPath(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
			}
		}
	})
	t.Run("transitionPath returns null when the target is unreachable", func(t *testing.T) {
		// DEVIATION: six states (plan §Ticket states). The closed table reaches every status from every
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
		// DEVIATION: six states (plan §Ticket states). The allowed list from BACKLOG is READY alone.
		err := InvalidTransition(Backlog, Done, AllowedNext(Backlog), ShortestPath(Backlog, Done))
		if err.Code != CodeInvalidTransition {
			t.Fatalf("code = %s", err.Code)
		}
		for _, want := range []string{"From BACKLOG you can move to: READY.", "BACKLOG → READY → IN_PROGRESS → REVIEW → DONE"} {
			if !strings.Contains(err.Message, want) {
				t.Fatalf("message %q lacks %q", err.Message, want)
			}
		}
		if err.Details["from"] != Backlog || err.Details["to"] != Done || !reflect.DeepEqual(err.Details["allowed"], []Status{Ready}) {
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
	})
}

// TestInvalidTransitionReviewToBacklog: the guidance names REVIEW's two moves and the shortest route
// back to BACKLOG.
func TestInvalidTransitionReviewToBacklog(t *testing.T) {
	if CanTransition(Review, Backlog) {
		t.Fatal("REVIEW->BACKLOG is legal")
	}
	err := InvalidTransition(Review, Backlog, AllowedNext(Review), ShortestPath(Review, Backlog))
	if got := err.Details["allowed"]; !reflect.DeepEqual(got, []Status{Done, InProgress}) {
		t.Fatalf("allowed = %v", got)
	}
	if got := err.Details["path"]; !reflect.DeepEqual(got, []Status{Review, InProgress, Ready, Backlog}) {
		t.Fatalf("path = %v", got)
	}
	if !strings.Contains(err.Message, "REVIEW → IN_PROGRESS → READY → BACKLOG") {
		t.Fatalf("message = %q", err.Message)
	}
}
