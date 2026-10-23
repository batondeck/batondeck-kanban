//go:build darwin

package core

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred is the connected peer's uid, from LOCAL_PEERCRED (the standard library has no xucred),
// and its pid, from LOCAL_PEERPID.
func peerCred(c *net.UnixConn) (Peer, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *unix.Xucred
	var pid int
	var serr error
	if err := raw.Control(func(fd uintptr) {
		if cred, serr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED); serr == nil {
			pid, serr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	}); err != nil {
		return Peer{}, err
	}
	if serr != nil {
		return Peer{}, serr
	}
	return Peer{UID: int(cred.Uid), PID: pid}, nil
}
