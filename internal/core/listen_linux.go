//go:build linux

package core

import (
	"net"
	"syscall"
)

// peerCred is the connected peer's uid and pid, from SO_PEERCRED.
func peerCred(c *net.UnixConn) (Peer, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return Peer{}, err
	}
	if serr != nil {
		return Peer{}, serr
	}
	return Peer{UID: int(cred.Uid), PID: int(cred.Pid)}, nil
}
