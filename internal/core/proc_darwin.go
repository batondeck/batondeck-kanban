//go:build darwin

package core

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

// ProcessUID is the uid owning pid, from sysctl kern.proc.pid; ErrNoProcess when no live process
// has it (a zombie counts as exited).
func ProcessUID(pid int) (int, error) {
	if pid <= 0 {
		return 0, ErrNoProcess
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid || kp.Proc.P_stat == sZOMB {
		return 0, ErrNoProcess
	}
	return int(kp.Eproc.Ucred.Uid), nil
}

// ProcessParent is pid's parent pid and the name of the program it runs (p_comm, which for a
// script is its interpreter's), from sysctl kern.proc.pid; ErrNoProcess when pid is gone.
func ProcessParent(pid int) (ppid int, name string, err error) {
	if pid <= 0 {
		return 0, "", ErrNoProcess
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid || kp.Proc.P_stat == sZOMB {
		return 0, "", ErrNoProcess
	}
	comm := make([]byte, 0, len(kp.Proc.P_comm))
	for _, c := range kp.Proc.P_comm {
		if c == 0 {
			break
		}
		comm = append(comm, byte(c))
	}
	return int(kp.Eproc.Ppid), string(comm), nil
}

// sZOMB is <sys/proc.h>'s SZOMB, a process that exited and was not yet reaped.
const sZOMB = 5

// kqueueWatch is one kqueue holding an EVFILT_PROC NOTE_EXIT filter per watched pid and an
// EVFILT_USER filter that close triggers to end the loop. quit, closed by close, ends a send on
// exited that nobody reads.
type kqueueWatch struct {
	kq     int
	mu     sync.Mutex
	pids   map[int]struct{}
	closed bool
	quit   chan struct{}
	done   chan struct{}
}

const wakeIdent = 0

func newExitImpl(exited chan<- int) (exitImpl, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("exit watch: kqueue: %w", err)
	}
	unix.CloseOnExec(kq)
	user := []unix.Kevent_t{{Ident: wakeIdent, Filter: unix.EVFILT_USER, Flags: unix.EV_ADD | unix.EV_CLEAR}}
	if _, err := unix.Kevent(kq, user, nil, nil); err != nil {
		unix.Close(kq)
		return nil, fmt.Errorf("exit watch: kevent: %w", err)
	}
	w := &kqueueWatch{kq: kq, pids: map[int]struct{}{}, quit: make(chan struct{}), done: make(chan struct{})}
	go w.loop(exited)
	return w, nil
}

func (w *kqueueWatch) loop(exited chan<- int) {
	defer close(w.done)
	events := make([]unix.Kevent_t, 16)
	for {
		n, err := unix.Kevent(w.kq, nil, events, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return
		}
		for _, ev := range events[:n] {
			if ev.Filter == unix.EVFILT_USER {
				return
			}
			if ev.Filter != unix.EVFILT_PROC || ev.Fflags&unix.NOTE_EXIT == 0 {
				continue
			}
			pid := int(ev.Ident)
			w.mu.Lock()
			_, watched := w.pids[pid]
			delete(w.pids, pid)
			w.mu.Unlock()
			if watched {
				select {
				case exited <- pid:
				case <-w.quit:
					return
				}
			}
		}
	}
}

func (w *kqueueWatch) add(pid int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("exit watch: closed")
	}
	if _, ok := w.pids[pid]; ok {
		return nil
	}
	ch := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	if _, err := unix.Kevent(w.kq, ch, nil, nil); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return ErrNoProcess
		}
		return fmt.Errorf("exit watch: watching pid %d: %w", pid, err)
	}
	w.pids[pid] = struct{}{}
	return nil
}

func (w *kqueueWatch) remove(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.pids[pid]; !ok || w.closed {
		return
	}
	delete(w.pids, pid)
	ch := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_DELETE}}
	_, _ = unix.Kevent(w.kq, ch, nil, nil) // ENOENT: it exited meanwhile, and loop drops the event
}

func (w *kqueueWatch) close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	close(w.quit)
	trig := []unix.Kevent_t{{Ident: wakeIdent, Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}
	if _, err := unix.Kevent(w.kq, trig, nil, nil); err != nil {
		return fmt.Errorf("exit watch: waking the loop: %w", err)
	}
	<-w.done
	return unix.Close(w.kq)
}
