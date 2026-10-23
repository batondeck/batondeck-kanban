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
