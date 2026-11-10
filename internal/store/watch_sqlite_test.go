package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A SQLite watch hears a commit made by another connection on the file that fires no in-process
// signal, which is what a second `serve` process on the same data dir is (core/lock.go admits several
// on one host): its tail was never woken by the other's writes, so its board snapshots went stale
// and its doorbells stayed silent. The watch wakes once at start, once for the foreign commit, and
// not again while nothing commits.
func TestSQLiteWatchHearsAnotherProcessCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.db")
	s, err := OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.w.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	woke := make(chan struct{}, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.WatchChanges(ctx, func() { woke <- struct{}{} }) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("WatchChanges: %v", err)
		}
	}()
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake at the start of the watch")
	}
	quietFor := func(d time.Duration) {
		t.Helper()
		select {
		case <-woke:
			t.Fatal("a wake with nothing committed")
		case <-time.After(d):
		}
	}
	quietFor(3 * sqliteWatchPoll)

	// Another process: a connection of its own on the file, outside this store and its signal.
	other, err := sql.Open("sqlite", sqliteDSN(path, "FULL", true))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec("INSERT INTO t (n) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not hear another process's commit")
	}
	quietFor(3 * sqliteWatchPoll)

	// This process's own commit wakes it once, by the in-process signal, not again by the poll.
	if _, err := s.w.Exec("INSERT INTO t (n) VALUES (2)"); err != nil {
		t.Fatal(err)
	}
	s.noteAppended()
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not hear this process's commit")
	}
	quietFor(3 * sqliteWatchPoll)
}
