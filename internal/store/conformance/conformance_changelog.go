package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("changelog", changelog) }

func probeChangeLog(s store.Store) error {
	_, err := s.ReadChanges(context.Background(), 0, 1)
	return err
}

func changeKinds(cs []store.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Kind
	}
	return out
}

// changelog is the change log the bus tails: ids that rise with each append, reads after a cursor
// of every scope and of one, nothing kept by a rolled-back transaction, and the retention delete;
// each with its refusals.
func changelog(t *testing.T, newStore Factory) {
	ctx := context.Background()
	requireBuilt(t, newStore, probeChangeLog)

	appendKinds := func(t *testing.T, s store.Store, scope string, kinds ...string) []int64 {
		t.Helper()
		ids := make([]int64, len(kinds))
		for i, k := range kinds {
			id, err := s.AppendChange(ctx, store.ChangeInput{Scope: scope, Kind: k, Ts: taskClock + int64(i)})
			mustOK(t, err)
			ids[i] = id
		}
		return ids
	}

	t.Run("AppendChangeIdsRiseAndReadBack", func(t *testing.T) {
		s := migrated(t, newStore)
		id, err := s.AppendChange(ctx, store.ChangeInput{Scope: "ws", ProjectID: "P-1", Kind: "task", Payload: json.RawMessage(`{"taskId":"T-1"}`), Ts: taskClock})
		mustOK(t, err)
		got, err := s.ReadChanges(ctx, 0, 10)
		mustOK(t, err)
		want := store.Change{ID: id, Scope: "ws", ProjectID: ptr("P-1"), Kind: "task", Payload: json.RawMessage(`{"taskId":"T-1"}`), Ts: taskClock}
		if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Fatalf("read back %+v, want %+v", got, want)
		}
		ids := appendKinds(t, s, "ws", "a", "b", "c")
		prev := id
		for _, n := range ids {
			if n <= prev {
				t.Fatalf("ids %d then %v: each must be higher than the last", id, ids)
			}
			prev = n
		}
		bare, err := s.ReadChanges(ctx, id, 1)
		mustOK(t, err)
		if len(bare) != 1 || bare[0].ProjectID != nil || string(bare[0].Payload) != `{}` {
			t.Fatalf("defaults: %+v", bare)
		}
	})

	t.Run("ReadChangesIsAfterTheCursorOldestFirstAndLimited", func(t *testing.T) {
		s := migrated(t, newStore)
		ids := appendKinds(t, s, "ws", "a", "b", "c", "d")
		after, err := s.ReadChanges(ctx, ids[1], 0)
		mustOK(t, err)
		if got := changeKinds(after); !reflect.DeepEqual(got, []string{"c", "d"}) {
			t.Fatalf("after the second: %v", got)
		}
		first, err := s.ReadChanges(ctx, 0, 2)
		mustOK(t, err)
		if got := changeKinds(first); !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("limit 2: %v", got)
		}
		none, err := s.ReadChanges(ctx, ids[3], 10)
		mustOK(t, err)
		if len(none) != 0 {
			t.Fatalf("after the last: %v", changeKinds(none))
		}
	})

	t.Run("ReadChangesForScopeIsOneScopes", func(t *testing.T) {
		s := migrated(t, newStore)
		a := appendKinds(t, s, "alice", "a1", "a2")
		appendKinds(t, s, "bob", "b1")
		appendKinds(t, s, "alice", "a3")
		got, err := s.ReadChangesForScope(ctx, "alice", 0, 10)
		mustOK(t, err)
		if kinds := changeKinds(got); !reflect.DeepEqual(kinds, []string{"a1", "a2", "a3"}) {
			t.Fatalf("alice's: %v", kinds)
		}
		got, err = s.ReadChangesForScope(ctx, "alice", a[0], 1)
		mustOK(t, err)
		if kinds := changeKinds(got); !reflect.DeepEqual(kinds, []string{"a2"}) {
			t.Fatalf("alice's after a1, limit 1: %v", kinds)
		}
		got, err = s.ReadChangesForScope(ctx, "carol", 0, 10)
		mustOK(t, err)
		if len(got) != 0 {
			t.Fatalf("carol's: %v", changeKinds(got))
		}
	})

	t.Run("AnAppendInARolledBackTransactionIsNeverRead", func(t *testing.T) {
		s := migrated(t, newStore)
		before := appendKinds(t, s, "ws", "kept")
		boom := errors.New("boom")
		err := s.Atomically(ctx, func(tx store.Store) error {
			if _, err := tx.AppendChange(ctx, store.ChangeInput{Scope: "ws", Kind: "gone", Ts: taskClock}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Atomically = %v", err)
		}
		after := appendKinds(t, s, "ws", "next")
		if after[0] <= before[0] {
			t.Fatalf("the id after a rollback %d is not above %d", after[0], before[0])
		}
		got, err := s.ReadChanges(ctx, 0, 10)
		mustOK(t, err)
		if kinds := changeKinds(got); !reflect.DeepEqual(kinds, []string{"kept", "next"}) {
			t.Fatalf("after a rollback: %v", kinds)
		}
	})

	t.Run("AppendChangeRefusals", func(t *testing.T) {
		s := migrated(t, newStore)
		for name, in := range map[string]store.ChangeInput{
			"no scope":              {Kind: "k", Ts: taskClock},
			"no kind":               {Scope: "ws", Ts: taskClock},
			"no time":               {Scope: "ws", Kind: "k"},
			"a payload not object":  {Scope: "ws", Kind: "k", Ts: taskClock, Payload: json.RawMessage(`"x"`)},
			"a payload not JSON":    {Scope: "ws", Kind: "k", Ts: taskClock, Payload: json.RawMessage(`{`)},
			"a payload JSON null'd": {Scope: "ws", Kind: "k", Ts: taskClock, Payload: json.RawMessage(`null`)},
		} {
			_, err := s.AppendChange(ctx, in)
			if err == nil {
				t.Fatalf("%s: appended", name)
			}
			wantCode(t, err, domain.CodeValidation, "")
		}
		if got, err := s.ReadChanges(ctx, 0, 10); err != nil || len(got) != 0 {
			t.Fatalf("a refusal appended %v %v", changeKinds(got), err)
		}
	})

	t.Run("DeleteChangesOlderThanDeletesTheOldestUpToTheLimit", func(t *testing.T) {
		s := migrated(t, newStore)
		appendKinds(t, s, "ws", "t0", "t1", "t2", "t3") // ts taskClock+0..3
		n, err := s.DeleteChangesOlderThan(ctx, taskClock+3, 2)
		mustOK(t, err)
		if n != 2 {
			t.Fatalf("deleted %d, want 2 (the limit)", n)
		}
		got, _ := s.ReadChanges(ctx, 0, 10)
		if kinds := changeKinds(got); !reflect.DeepEqual(kinds, []string{"t2", "t3"}) {
			t.Fatalf("after the first sweep: %v", kinds)
		}
		n, err = s.DeleteChangesOlderThan(ctx, taskClock+3, 10)
		mustOK(t, err)
		if n != 1 {
			t.Fatalf("deleted %d, want 1 (t3 is not older than the bound)", n)
		}
		got, _ = s.ReadChanges(ctx, 0, 10)
		if kinds := changeKinds(got); !reflect.DeepEqual(kinds, []string{"t3"}) {
			t.Fatalf("after the second sweep: %v", kinds)
		}
		_, err = s.DeleteChangesOlderThan(ctx, taskClock+10, 0)
		wantCode(t, err, domain.CodeValidation, "")
	})
}
