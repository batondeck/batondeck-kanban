package conformance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// counters is the per-project task counter: T-1 first, per project, unique under concurrency, and
// part of the transaction it runs in.
func counters(t *testing.T, newStore Factory) {
	ctx := context.Background()

	next := func(t *testing.T, s store.Store, projectID string) string {
		t.Helper()
		id, err := s.NextTaskID(ctx, projectID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Run("NextTaskIDCountsPerProject", func(t *testing.T) {
		s := migrated(t, newStore)
		a := newProject(t, s, "alice")
		b := newProject(t, s, "alice")
		for _, step := range []struct{ project, want string }{
			{a.ID, "T-1"}, {a.ID, "T-2"}, {b.ID, "T-1"}, {a.ID, "T-3"}, {b.ID, "T-2"},
		} {
			if got := next(t, s, step.project); got != step.want {
				t.Fatalf("NextTaskID(%s) = %s, want %s", step.project, got, step.want)
			}
		}
	})

	t.Run("OneHundredConcurrentCallsGetTOneToTOneHundredOnce", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		const n = 100
		ids := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ids[i], errs[i] = s.NextTaskID(ctx, p.ID)
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("%s was handed out twice", id)
			}
			seen[id] = true
		}
		for i := 1; i <= n; i++ {
			if !seen[fmt.Sprintf("T-%d", i)] {
				t.Fatalf("T-%d was never handed out: got %v", i, ids)
			}
		}
		if got := next(t, s, newProject(t, s, "bob").ID); got != "T-1" {
			t.Fatalf("a second project starts at %s, want T-1", got)
		}
	})

	t.Run("NextTaskIDIsPartOfTheTransaction", func(t *testing.T) {
		s := migrated(t, newStore)
		p := newProject(t, s, "alice")
		boom := errors.New("boom")
		err := s.Atomically(ctx, func(tx store.Store) error {
			if _, err := tx.NextTaskID(ctx, p.ID); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Atomically = %v, want the callback's error", err)
		}
		if got := next(t, s, p.ID); got != "T-1" {
			t.Fatalf("after a rolled-back call the next id is %s, want T-1 (no committed row held it)", got)
		}
	})

	t.Run("NextTaskIDOfAMissingProjectIsNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		_, err := s.NextTaskID(ctx, "P-missing")
		wantCode(t, err, domain.CodeNotFound, "Project")
	})
}
