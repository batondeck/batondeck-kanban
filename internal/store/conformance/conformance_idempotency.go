package conformance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// idempotency is the replay of a creating mutation: within the window the same result and one row,
// after it a second, scoped by identity, nothing kept by a failed call, and one run under concurrency.
func idempotency(t *testing.T, newStore Factory) {
	ctx := context.Background()
	in := store.CreateProjectInput{Name: "p", CreatedBy: "alice"}

	owned := func(t *testing.T, s store.Store, identity string) int {
		t.Helper()
		ps, err := s.ListProjects(ctx, identity)
		if err != nil {
			t.Fatal(err)
		}
		return len(ps)
	}

	t.Run("CreateProjectReplayedInsideTheWindowIsTheSameProjectAndOneRow", func(t *testing.T) {
		s := migrated(t, newStore)
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "k1", IdempotencyTTL: time.Hour}
		first, err := s.CreateProject(ctx, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.CreateProject(ctx, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		if second.ID != first.ID || second.CreatedAt != first.CreatedAt {
			t.Fatalf("replay = %+v, want %+v", second, first)
		}
		if n := owned(t, s, "alice"); n != 1 {
			t.Fatalf("%d projects after a replay, want 1", n)
		}
	})

	t.Run("CreateProjectAfterTheWindowCreatesASecond", func(t *testing.T) {
		s := migrated(t, newStore)
		// The first key's window is short, and the sleep after its call returns outlasts it by
		// 50 ms: the sleep alone bounds that expiry, however loaded the machine (a late wake only
		// widens the margin).
		const window = 100 * time.Millisecond
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "k1", IdempotencyTTL: window}
		first, err := s.CreateProject(ctx, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(window + 50*time.Millisecond)
		// The renewal takes an hour's window, so the third call's replay does not depend on how
		// soon it follows the second: under gate load a 100 ms window lapsed between the two and
		// the third created a project (P-94, P-157).
		renewed := opts
		renewed.IdempotencyTTL = time.Hour
		second, err := s.CreateProject(ctx, in, renewed)
		if err != nil {
			t.Fatal(err)
		}
		if second.ID == first.ID {
			t.Fatalf("after the window the key replayed %s", first.ID)
		}
		if n := owned(t, s, "alice"); n != 2 {
			t.Fatalf("%d projects after the window, want 2", n)
		}
		third, err := s.CreateProject(ctx, in, renewed)
		if err != nil || third.ID != second.ID {
			t.Fatalf("the renewed key replayed %+v, %v: want %s", third, err, second.ID)
		}
	})

	t.Run("DeleteExpiredIdempotencyKeysSweepsThePastWindowsAPageAtATime", func(t *testing.T) {
		s := migrated(t, newStore)
		short := store.WriteOptions{Identity: "alice", IdempotencyTTL: time.Millisecond}
		p := newProject(t, s, "alice")
		for _, key := range []string{"a", "b", "c"} {
			o := short
			o.IdempotencyKey = key
			if _, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: key, CreatedBy: "alice"}, o); err != nil {
				t.Fatal(err)
			}
		}
		global := short
		global.IdempotencyKey = "cp"
		if _, err := s.CreateProject(ctx, in, global); err != nil {
			t.Fatal(err)
		}
		live := store.WriteOptions{Identity: "alice", IdempotencyKey: "live", IdempotencyTTL: time.Hour}
		kept, err := s.CreateProject(ctx, in, live)
		if err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(time.Minute).UnixMilli()
		for _, page := range []struct {
			limit int
			want  int64
		}{{3, 3}, {3, 1}, {3, 0}} {
			if n, err := s.DeleteExpiredIdempotencyKeys(ctx, later, page.limit); err != nil || n != page.want {
				t.Fatalf("sweep of %d: %d (%v), want %d", page.limit, n, err, page.want)
			}
		}
		if again, err := s.CreateProject(ctx, in, live); err != nil || again.ID != kept.ID {
			t.Fatalf("the key in its window replayed %+v (%v), want %s", again, err, kept.ID)
		}
		_, err = s.DeleteExpiredIdempotencyKeys(ctx, later, 0)
		wantCode(t, err, domain.CodeValidation, "")
	})

	t.Run("AKeyIsScopedByIdentity", func(t *testing.T) {
		s := migrated(t, newStore)
		a, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "p", CreatedBy: "alice"}, store.WriteOptions{Identity: "alice", IdempotencyKey: "k", IdempotencyTTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "p", CreatedBy: "bob"}, store.WriteOptions{Identity: "bob", IdempotencyKey: "k", IdempotencyTTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if a.ID == b.ID || b.CreatedBy != "bob" {
			t.Fatalf("bob's call with alice's key got %+v", b)
		}
	})

	t.Run("WithoutAKeyEveryCallCreates", func(t *testing.T) {
		s := migrated(t, newStore)
		for range 2 {
			if _, err := s.CreateProject(ctx, in, store.WriteOptions{Identity: "alice"}); err != nil {
				t.Fatal(err)
			}
		}
		if n := owned(t, s, "alice"); n != 2 {
			t.Fatalf("%d projects, want 2", n)
		}
	})

	t.Run("AFailedCallKeepsNothingAndTheKeyRunsAgain", func(t *testing.T) {
		s := migrated(t, newStore)
		boom := errors.New("boom")
		_, err := s.WithIdempotency(ctx, "P-x", "alice", "k", time.Hour, func(tx store.Store) ([]byte, error) {
			if _, err := tx.CreateProject(ctx, in, store.WriteOptions{Identity: "alice"}); err != nil {
				return nil, err
			}
			return nil, boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("WithIdempotency = %v, want the callback's error", err)
		}
		if n := owned(t, s, "alice"); n != 0 {
			t.Fatalf("a failed call left %d projects", n)
		}
		got, err := s.WithIdempotency(ctx, "P-x", "alice", "k", time.Hour, func(store.Store) ([]byte, error) { return []byte(`"ran"`), nil })
		if err != nil || string(got) != `"ran"` {
			t.Fatalf("the key after a failure: %s, %v", got, err)
		}
		again, err := s.WithIdempotency(ctx, "P-x", "alice", "k", time.Hour, func(store.Store) ([]byte, error) { return []byte(`"twice"`), nil })
		if err != nil || string(again) != `"ran"` {
			t.Fatalf("the replay: %s, %v, want the kept result", again, err)
		}
	})

	t.Run("ConcurrentCallsWithOneKeyRunOnce", func(t *testing.T) {
		s := migrated(t, newStore)
		const n = 10
		opts := store.WriteOptions{Identity: "alice", IdempotencyKey: "race", IdempotencyTTL: time.Hour}
		ids := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p, err := s.CreateProject(ctx, in, opts)
				ids[i], errs[i] = p.ID, err
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if id != ids[0] {
				t.Fatalf("one key gave several projects: %v", ids)
			}
		}
		if got := owned(t, s, "alice"); got != 1 {
			t.Fatalf("%d projects from one key, want 1", got)
		}
	})

	t.Run("AKeyWithoutAWindowIsValidation", func(t *testing.T) {
		s := migrated(t, newStore)
		_, err := s.CreateProject(ctx, in, store.WriteOptions{Identity: "alice", IdempotencyKey: "k"})
		wantCode(t, err, domain.CodeValidation, "")
	})
}
