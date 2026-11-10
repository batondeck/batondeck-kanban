//go:build !linux && !darwin

package core

import (
	"errors"
	"net"
)

// peerCred has no peer-credential read on this platform, so every Unix connection is refused.
func peerCred(*net.UnixConn) (Peer, error) {
	return Peer{}, errors.New("no peer-credential check on this platform")
}

// MaxSocketPath is the longest Unix socket path this package lets a listen entry name on a
// platform it has no peer check for (the BSDs' sun_path is 104 bytes, one of them the NUL).
const MaxSocketPath = 103
