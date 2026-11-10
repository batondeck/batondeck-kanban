//go:build linux

package core

import (
	"os"
	"path/filepath"
	"testing"
)

// A Remove after Close (tested) writes nothing. serve closes the watcher when the directory's
// dispatcher returns, while its HTTP server still takes an unregister (or a re-register) that
// removes a pid: the kick then went to the closed eventfd's number, which the next open had handed
// to an unrelated file, socket or database connection.
func TestExitWatcherRemoveAfterCloseWritesNothing(t *testing.T) {
	cmd := startSleeper(t)
	w, err := NewExitWatcher()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	efd := w.impl.(*pidfdWatch).efd
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Open files until one takes the eventfd's number (the lowest free descriptor is handed out).
	dir := t.TempDir()
	var victim *os.File
	var others []*os.File
	for i := 0; i < 64 && victim == nil; i++ {
		f, err := os.Create(filepath.Join(dir, "victim"))
		if err != nil {
			t.Fatal(err)
		}
		if int(f.Fd()) == efd {
			victim = f
		} else {
			others = append(others, f)
		}
	}
	for _, f := range others {
		f.Close()
	}
	if victim == nil {
		t.Fatalf("no file took the eventfd's number %d", efd)
	}
	defer victim.Close()
	w.Remove(cmd.Process.Pid)
	if err := w.Add(cmd.Process.Pid); err == nil {
		t.Fatal("Add after Close succeeded")
	}
	b, err := os.ReadFile(victim.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Fatalf("Remove after Close wrote %d bytes %v into an unrelated file that reused fd %d", len(b), b, efd)
	}
}
