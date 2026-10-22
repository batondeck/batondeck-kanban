package domain

import (
	"reflect"
	"testing"
)

// The auto-unblock guard, both ways (plan §Fold 1, §Verification "a poison task whose last dependency
// completes stays BLOCKED"). Conductor's autoUnblock (src/domain/unblock.ts) is a Store loop and has no
// unit table; the guard at its line 35 is what is ported, with the poison clause added.

func TestUnblock(t *testing.T) {
	t.Run("refusal: poison with empty blockedBy stays BLOCKED", func(t *testing.T) {
		if AutoReady(nil, Blocked, BlockedReasonPoison) {
			t.Fatal("a poisoned task whose last dependency cleared must stay BLOCKED")
		}
	})
	t.Run("control: non-poison with empty blockedBy becomes READY", func(t *testing.T) {
		if !AutoReady(nil, Blocked, "dependency") || !AutoReady([]string{}, Blocked, "") {
			t.Fatal("a dependency-blocked task whose last blocker cleared must become READY")
		}
	})
	t.Run("still blocked by another task", func(t *testing.T) {
		if AutoReady([]string{"T-2"}, Blocked, "") {
			t.Fatal("a task with a blocker left stays BLOCKED")
		}
	})
	t.Run("only BLOCKED tasks move", func(t *testing.T) {
		for _, s := range AllStatuses() {
			if s != Blocked && AutoReady(nil, s, "") {
				t.Errorf("%s must not be auto-readied", s)
			}
		}
	})
}

func TestDecideUnblock(t *testing.T) {
	d := DecideUnblock([]string{"T-1", "T-2"}, Blocked, "", "T-1")
	if !d.Cleared || !reflect.DeepEqual(d.BlockedBy, []string{"T-2"}) || d.AutoReady {
		t.Fatalf("clearing one of two blockers: %+v", d)
	}
	d = DecideUnblock([]string{"T-2"}, Blocked, "", "T-2")
	if !d.Cleared || len(d.BlockedBy) != 0 || !d.AutoReady {
		t.Fatalf("clearing the last blocker: %+v", d)
	}
	d = DecideUnblock([]string{"T-2"}, Blocked, BlockedReasonPoison, "T-2")
	if !d.Cleared || len(d.BlockedBy) != 0 || d.AutoReady {
		t.Fatalf("clearing a poisoned task's last blocker must not ready it: %+v", d)
	}
	d = DecideUnblock([]string{"T-2"}, Blocked, "", "T-3")
	if d.Cleared || !reflect.DeepEqual(d.BlockedBy, []string{"T-2"}) || d.AutoReady {
		t.Fatalf("an id that is not a blocker writes nothing: %+v", d)
	}
}
