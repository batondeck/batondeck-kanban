package core

import (
	"errors"
	"sync"
)

// Processes on this device (ADR-0004 "the session directory"): the owner of a pid, and a watch
// that reports the moment a watched pid exits (kqueue EVFILT_PROC NOTE_EXIT on darwin, a pidfd and
// poll on Linux), so a crashed client leaves the directory with no hook run.

// ErrNoProcess is a pid that names no live process.
var ErrNoProcess = errors.New("no such process")

// ExitWatcher watches pids for exit. Exited receives each watched pid once, when it exits; Remove
// stops watching one. Make one with NewExitWatcher and Close it when done.
type ExitWatcher struct {
	exited chan int
	once   sync.Once
	impl   exitImpl
}

// exitImpl is the platform half: add starts watching pid (ErrNoProcess when it has already
// exited), remove stops, close ends the watch loop, which sends on exited until then.
type exitImpl interface {
	add(pid int) error
	remove(pid int)
	close() error
}

// NewExitWatcher starts the platform's watch loop.
func NewExitWatcher() (*ExitWatcher, error) {
	w := &ExitWatcher{exited: make(chan int, 64)}
	impl, err := newExitImpl(w.exited)
	if err != nil {
		return nil, err
	}
	w.impl = impl
	return w, nil
}

// Add watches pid; ErrNoProcess when it is not alive. Adding a watched pid again is a no-op.
func (w *ExitWatcher) Add(pid int) error {
	if pid <= 0 {
		return ErrNoProcess
	}
	return w.impl.add(pid)
}

// Remove stops watching pid; an unwatched pid is a no-op.
func (w *ExitWatcher) Remove(pid int) { w.impl.remove(pid) }

// Exited is the pids that exited, each once. It is never closed; read it until Close.
func (w *ExitWatcher) Exited() <-chan int { return w.exited }

// Close stops the watch loop. It is idempotent.
func (w *ExitWatcher) Close() error {
	var err error
	w.once.Do(func() { err = w.impl.close() })
	return err
}
