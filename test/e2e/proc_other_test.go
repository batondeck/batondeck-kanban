//go:build e2e && !linux

package e2e

import "syscall"

// serveProcAttr is nothing off Linux, which has no parent-death signal: there a `go test -timeout`
// can leave serve running.
func serveProcAttr() *syscall.SysProcAttr { return nil }
