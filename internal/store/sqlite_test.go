package store

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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

// A path is a path, not a URI: a '#', '?', '%' or space in it, or a leading '//', names the file
// opened, on the write connection and the read pool alike, and both pools run every pragma
// (foreign_keys on). Pasted unescaped into the file: URI, '#' and '?' cut the path short (the
// database landed in the parent directory, outside the data dir), '?' swallowed the first pragma,
// and '%41' was decoded or refused.
func TestOpenSQLiteOpensExactlyThePathNamed(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	for _, rel := range []string{"team#1/bk.db", "what?x=1/bk.db", "pct%41x/100%25.db", "a space/bk.db", "mixed #?%/bk.db"} {
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		s, err := OpenSQLite(path, "FULL")
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if _, err := s.w.ExecContext(ctx, "CREATE TABLE marker (n INTEGER)"); err != nil {
			s.Close()
			t.Fatalf("%s: the write connection: %v", rel, err)
		}
		for name, db := range map[string]*sql.DB{"write": s.w, "read": s.r} {
			var fk int
			if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
				t.Errorf("%s: the %s pool's foreign_keys = %d, %v; want 1", rel, name, fk, err)
			}
			var n int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM marker").Scan(&n); err != nil {
				t.Errorf("%s: the %s pool does not see the table the write connection made: %v", rel, name, err)
			}
		}
		s.Close()
		if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
			t.Errorf("%s: the file named is not the database: %v", rel, err)
		}
	}
	// A path that starts with two slashes is the same file as with one, not a URI authority.
	s, err := OpenSQLite("/"+filepath.Join(base, "slashes.db"), "FULL")
	if err != nil {
		t.Fatalf("a path starting //: %v", err)
	}
	if _, err := s.w.ExecContext(ctx, "CREATE TABLE marker (n INTEGER)"); err != nil {
		t.Fatalf("a path starting //: %v", err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(base, "slashes.db")); err != nil {
		t.Errorf("a path starting //: %v", err)
	}
	// A relative path is relative to the working directory, '#' and ':' included.
	if err := os.Mkdir(filepath.Join(base, "rel"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(base, "rel"))
	if s, err = OpenSQLite("a:b#1.db", "FULL"); err != nil {
		t.Fatalf("a relative path: %v", err)
	}
	if _, err := s.w.ExecContext(ctx, "CREATE TABLE marker (n INTEGER)"); err != nil {
		t.Fatalf("a relative path: %v", err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(base, "rel", "a:b#1.db")); err != nil {
		t.Errorf("a relative path: %v", err)
	}
	// Nothing but the directories made above and the files in them: no database landed beside them.
	var stray []string
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Dir(p) == base && !strings.HasPrefix(d.Name(), "slashes.db") {
			stray = append(stray, p)
		}
		return nil
	})
	if len(stray) > 0 {
		t.Fatalf("files outside the directories named: %v", stray)
	}
}

// The database file OpenSQLite creates is private (0600), and so are the -wal and -shm files SQLite
// makes beside it, which take the database file's mode, whatever the umask lets through and
// whatever the directory's mode: it holds every task, comment, the audit log and the password
// hashes, and the README's example puts it in a directory made by a plain mkdir (0755).
func TestOpenSQLiteCreatesTheFilesPrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "srv")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bk.db")
	s, err := OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.w.Exec("CREATE TABLE x (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.r.QueryRow("SELECT count(*) FROM x").Scan(&n); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(p), mode)
		}
	}
}
