//go:build linux

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ProcessUID is the uid owning pid, the owner of /proc/<pid>; ErrNoProcess when no live process
// has it (a zombie counts as exited).
func ProcessUID(pid int) (int, error) {
	if pid <= 0 {
		return 0, ErrNoProcess
	}
	dir := "/proc/" + strconv.Itoa(pid)
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, ErrNoProcess
	}
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return 0, ErrNoProcess
	}
	// The state is the field after the command, which is in parentheses and may hold any byte.
	if i := strings.LastIndexByte(string(stat), ')'); i < 0 || i+2 >= len(stat) || stat[i+2] == 'Z' || stat[i+2] == 'X' {
		return 0, ErrNoProcess
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("process %d: no owner in its stat", pid)
	}
	return int(st.Uid), nil
}

// ProcessParent is pid's parent pid, from the field after the state in /proc/<pid>/stat, and the
// name of the program it runs: the base name of /proc/<pid>/exe (a script's interpreter, where the
// command name would be the script's), else its command name. ErrNoProcess when pid is gone.
func ProcessParent(pid int) (ppid int, name string, err error) {
	if pid <= 0 {
		return 0, "", ErrNoProcess
	}
	dir := "/proc/" + strconv.Itoa(pid)
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return 0, "", ErrNoProcess
	}
	open, end := strings.IndexByte(string(stat), '('), strings.LastIndexByte(string(stat), ')')
	if open < 0 || end < open {
		return 0, "", fmt.Errorf("process %d: unreadable stat", pid)
	}
	fields := strings.Fields(string(stat[end+1:])) // state, ppid, ...
	if len(fields) < 2 {
		return 0, "", fmt.Errorf("process %d: unreadable stat", pid)
	}
	if ppid, err = strconv.Atoi(fields[1]); err != nil {
		return 0, "", fmt.Errorf("process %d: unreadable stat", pid)
	}
	name = string(stat[open+1 : end])
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		name = filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
	}
	return ppid, name, nil
}

// pidfdWatch holds a pidfd per watched pid and polls them with an eventfd that add, remove and
// close write to so the loop rebuilds its set. A removed pidfd is closed by the loop, after the
// poll that may still hold it returns. Once closed, add and remove touch no descriptor: the
// eventfd's number may already belong to another file.
type pidfdWatch struct {
	efd     int
	mu      sync.Mutex
	fds     map[int]int // pid → pidfd
	retired []int
	closed  bool
	done    chan struct{}
}

func newExitImpl(exited chan<- int) (exitImpl, error) {
	efd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("exit watch: eventfd: %w", err)
	}
	w := &pidfdWatch{efd: efd, fds: map[int]int{}, done: make(chan struct{})}
	go w.loop(exited)
	return w, nil
}

func (w *pidfdWatch) kick() {
	one := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	_, _ = unix.Write(w.efd, one)
}

func (w *pidfdWatch) loop(exited chan<- int) {
	defer close(w.done)
	buf := make([]byte, 8)
	for {
		w.mu.Lock()
		for _, fd := range w.retired {
			unix.Close(fd)
		}
		w.retired = w.retired[:0]
		if w.closed {
			for _, fd := range w.fds {
				unix.Close(fd)
			}
			clear(w.fds)
			w.mu.Unlock()
			return
		}
		set := []unix.PollFd{{Fd: int32(w.efd), Events: unix.POLLIN}}
		pids := []int{0}
		for pid, fd := range w.fds {
			set = append(set, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
			pids = append(pids, pid)
		}
		w.mu.Unlock()
		if _, err := unix.Poll(set, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		if set[0].Revents != 0 {
			_, _ = unix.Read(w.efd, buf)
		}
		for i := 1; i < len(set); i++ {
			if set[i].Revents == 0 {
				continue
			}
			pid := pids[i]
			w.mu.Lock()
			fd, watched := w.fds[pid]
			if watched && fd == int(set[i].Fd) {
				delete(w.fds, pid)
				w.retired = append(w.retired, fd)
			} else {
				watched = false
			}
			w.mu.Unlock()
			if watched {
				exited <- pid
			}
		}
	}
}

func (w *pidfdWatch) add(pid int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("exit watch: closed")
	}
	if _, ok := w.fds[pid]; ok {
		return nil
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return ErrNoProcess
		}
		return fmt.Errorf("exit watch: pidfd_open %d: %w", pid, err)
	}
	unix.CloseOnExec(fd)
	w.fds[pid] = fd
	w.kick()
	return nil
}

func (w *pidfdWatch) remove(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fd, ok := w.fds[pid]
	if !ok || w.closed {
		return
	}
	delete(w.fds, pid)
	w.retired = append(w.retired, fd)
	w.kick()
}

func (w *pidfdWatch) close() error {
	w.mu.Lock()
	w.closed = true
	w.kick()
	w.mu.Unlock()
	<-w.done
	return unix.Close(w.efd)
}
