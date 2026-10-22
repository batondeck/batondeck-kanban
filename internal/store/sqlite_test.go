package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

// Transactions that read and then write, from several goroutines at once, each on its own store
// over ONE file — what several processes are — must serialise at BEGIN and all commit. Opened as
// SQLite opens transactions by default, they do not: both read, the first writes and commits, and
// the second's upgrade fails SQLITE_BUSY at once, whatever busy_timeout says. Within one store the
// single write connection already serialises its goroutines, which is why each goroutine here has a
// store of its own: only BEGIN IMMEDIATE (`_txlock=immediate`) keeps them apart.
func TestWriteTransactionsOnOneFileSerialiseAtBegin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "immediate.db")
	const writers, each = 4, 3
	stores := make([]*SQLite, writers)
	for i := range stores {
		s, err := OpenSQLite(path, "FULL")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		stores[i] = s
	}
	if _, err := stores[0].w.ExecContext(ctx, "CREATE TABLE c (n INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, writers*each)
	for _, s := range stores {
		wg.Add(1)
		go func(s *SQLite) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				err := s.Atomically(ctx, func(tx Store) error {
					t := tx.(*SQLite).tx
					var n int
					if err := t.QueryRowContext(ctx, "SELECT count(*) FROM c").Scan(&n); err != nil {
						return err
					}
					// Long enough that, without the lock at BEGIN, another writer reads in between.
					time.Sleep(50 * time.Millisecond)
					_, err := t.ExecContext(ctx, "INSERT INTO c (n) VALUES (?)", n+1)
					return err
				})
				if err != nil {
					failures <- err
				}
			}
		}(s)
	}
	wg.Wait()
	close(failures)
	n := 0
	var first error
	for err := range failures {
		if first == nil {
			first = err
		}
		n++
	}
	if n > 0 {
		t.Fatalf("%d of %d write transactions failed, the first with: %v", n, writers*each, first)
	}
	// Serialised, each read the count the one before it committed: 1, 2, … with none repeated.
	var rows, distinct int
	if err := stores[0].r.QueryRowContext(ctx, "SELECT count(*), count(DISTINCT n) FROM c").Scan(&rows, &distinct); err != nil {
		t.Fatal(err)
	}
	if rows != writers*each || distinct != rows {
		t.Fatalf("%d rows, %d distinct counts; want %d of each", rows, distinct, writers*each)
	}
}

// The read pool refuses a write: only the one write connection writes.
func TestTheReadPoolRefusesWrites(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "ro.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.r.Exec("CREATE TABLE x (n INTEGER)"); err == nil {
		t.Fatal("a read connection created a table")
	}
	if _, err := s.w.Exec("CREATE TABLE x (n INTEGER)"); err != nil {
		t.Fatalf("the write connection: %v", err)
	}
}

// Two processes making goose's version table on one SQLite file for the first time must both
// succeed: the check for the table and its creation are one lock holder, so the second finds the
// table the first made instead of creating it again. That is the empty-migrations path
// (ensureVersionTable), so each store migrates an EMPTY directory here: with the binary's own
// migrations, two concurrent first migrations race in goose's Up instead, which this does not
// claim to hold. The file is put in WAL mode first, as any earlier open leaves it, so the two meet
// at BEGIN rather than at the journal-mode switch.
func TestFirstMigrationsOnOneFileBothSucceed(t *testing.T) {
	ctx := context.Background()
	const rounds = 30
	failed := 0
	var first error
	for r := 0; r < rounds; r++ {
		path := filepath.Join(t.TempDir(), "race.db")
		pre, err := OpenSQLite(path, "FULL")
		if err != nil {
			t.Fatal(err)
		}
		if err := pre.w.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		pre.Close()
		a, err := OpenSQLite(path, "FULL")
		if err != nil {
			t.Fatal(err)
		}
		b, err := OpenSQLite(path, "FULL")
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, s := range []*SQLite{a, b} {
			go func(s *SQLite) {
				<-start
				_, err := migrator{db: s.w, dialect: goose.DialectSQLite3, fsys: fstest.MapFS{}}.up(ctx)
				errs <- err
			}(s)
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				failed++
				if first == nil {
					first = err
				}
			}
		}
		var rows int
		if err := a.r.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version").Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("round %d: goose_db_version holds %d rows (%v), want its one version-0 row", r, rows, err)
		}
		a.Close()
		b.Close()
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent first migrations failed, the first with: %v", failed, 2*rounds, first)
	}
}
