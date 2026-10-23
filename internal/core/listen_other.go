//go:build !linux && !darwin

package core

import (
	"errors"
	"net"
)

// peerUID has no peer-credential read on this platform, so every Unix connection is refused.
func peerUID(*net.UnixConn) (int, error) {
	return 0, errors.New("no peer-credential check on this platform")
}
