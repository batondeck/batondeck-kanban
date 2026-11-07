//go:build e2e && linux

package e2e

import "syscall"

// serveProcAttr kills serve when the test binary dies: a `go test -timeout` exits without running
// cleanups, which would otherwise leave serve listening (the Postgres database is then not dropped
// either; its name is bk_e2e_<nanos>). Pdeathsig follows the thread that started serve; the runtime
// does not end its threads while the test runs, so serve outlives no part of it.
func serveProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
