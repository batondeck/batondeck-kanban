//go:build darwin || linux

package core

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// startSleeper is a child process the test owns and kills.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// A live pid is owned by its uid; a reaped one is ErrNoProcess.
func TestProcessUID(t *testing.T) {
	uid, err := ProcessUID(os.Getpid())
	if err != nil || uid != os.Getuid() {
		t.Fatalf("ProcessUID(self) = %d, %v; want %d", uid, err, os.Getuid())
	}
	cmd := startSleeper(t)
	pid := cmd.Process.Pid
	if uid, err := ProcessUID(pid); err != nil || uid != os.Getuid() {
		t.Fatalf("ProcessUID(child) = %d, %v", uid, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := ProcessUID(pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("ProcessUID(reaped) err = %v, want ErrNoProcess", err)
	}
	if _, err := ProcessUID(0); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("ProcessUID(0) err = %v", err)
	}
}

// A kill -9 of a watched pid is reported within a second, once; a removed pid is not reported; a pid
// that already exited is refused at Add.
func TestExitWatcherReportsAKill(t *testing.T) {
	w, err := NewExitWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	a, b := startSleeper(t), startSleeper(t)
	for _, c := range []*exec.Cmd{a, b} {
		if err := w.Add(c.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	w.Remove(b.Process.Pid)
	killed := time.Now()
	_ = a.Process.Kill()
	_ = b.Process.Kill()
	select {
	case pid := <-w.Exited():
		if pid != a.Process.Pid {
			t.Fatalf("exited %d, want %d", pid, a.Process.Pid)
		}
		if d := time.Since(killed); d > time.Second {
			t.Fatalf("reported %v after the kill, want under 1s", d)
		}
		t.Logf("exit of pid %d reported %v after kill -9", pid, time.Since(killed))
	case <-time.After(2 * time.Second):
		t.Fatal("the kill was never reported")
	}
	select {
	case pid := <-w.Exited():
		t.Fatalf("a second report: pid %d (the removed one was %d)", pid, b.Process.Pid)
	case <-time.After(200 * time.Millisecond):
	}
	_ = a.Wait()
	if err := w.Add(a.Process.Pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("Add(exited) = %v, want ErrNoProcess", err)
	}
}

// ProcessParent names a process's parent and the program it runs; a reaped pid is ErrNoProcess.
func TestProcessParent(t *testing.T) {
	cmd := startSleeper(t)
	ppid, name, err := ProcessParent(cmd.Process.Pid)
	if err != nil || ppid != os.Getpid() || name != "sleep" {
		t.Fatalf("ProcessParent(child) = %d, %q, %v; want %d, \"sleep\"", ppid, name, err, os.Getpid())
	}
	if ppid, _, err := ProcessParent(os.Getpid()); err != nil || ppid != os.Getppid() {
		t.Fatalf("ProcessParent(self) = %d, %v; want %d", ppid, err, os.Getppid())
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, _, err := ProcessParent(cmd.Process.Pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("ProcessParent(reaped) err = %v, want ErrNoProcess", err)
	}
}
