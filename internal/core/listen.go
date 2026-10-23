package core

// Listeners: every entry of the config's `listen` is a tcp:// or unix:// URL, and all of them serve
// one handler (plan §Sockets). A Unix socket is mode 0600 and checks the peer's uid on every
// connection it accepts (SO_PEERCRED on Linux, LOCAL_PEERCRED on macOS): a connection from another
// uid is closed before HTTP reads a byte, and logged. The bearer is still required for board
// identity; the socket only removes network exposure.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The networks a listen entry names.
const (
	NetworkTCP  = "tcp"
	NetworkUnix = "unix"
)

// ListenAddr is one parsed listen entry: Network tcp with Address host:port, or Network unix with
// Address an absolute socket path.
type ListenAddr struct {
	Network string
	Address string
}

// String is the entry as the config writes it.
func (a ListenAddr) String() string {
	if a.Network == NetworkUnix {
		return "unix://" + a.Address
	}
	return "tcp://" + a.Address
}

// ParseListen parses tcp://host:port (host may be empty: every interface) or unix:///abs/path.
func ParseListen(s string) (ListenAddr, error) {
	scheme, rest, ok := strings.Cut(s, "://")
	switch {
	case !ok:
		return ListenAddr{}, fmt.Errorf("listen %q: want tcp://host:port or unix:///path", s)
	case scheme == NetworkTCP:
		if _, port, err := net.SplitHostPort(rest); err != nil || port == "" {
			return ListenAddr{}, fmt.Errorf("listen %q: want tcp://host:port", s)
		}
		return ListenAddr{Network: NetworkTCP, Address: rest}, nil
	case scheme == NetworkUnix:
		u, err := url.Parse(s)
		if err != nil || u.Host != "" || !filepath.IsAbs(u.Path) || u.RawQuery != "" || u.Fragment != "" {
			return ListenAddr{}, fmt.Errorf("listen %q: want unix:///absolute/path", s)
		}
		return ListenAddr{Network: NetworkUnix, Address: filepath.Clean(u.Path)}, nil
	}
	return ListenAddr{}, fmt.Errorf("listen %q: unknown scheme %q (want tcp or unix)", s, scheme)
}

// Listen binds a. A Unix socket's directory is made 0700 when missing; a socket file nobody answers
// on (left by an unclean stop) is replaced, a live one or any other file at the path is refused;
// the socket is chmod 0600, and its listener admits only peers whose uid is the process's.
func Listen(a ListenAddr, logger *log.Logger) (net.Listener, error) {
	if a.Network == NetworkTCP {
		return net.Listen(NetworkTCP, a.Address)
	}
	return listenUnix(a.Address, os.Getuid(), logger)
}

// listenUnix is Listen for a socket path in a directory uid owns, admitting peers of uid alone.
func listenUnix(path string, uid int, logger *log.Logger) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("unix socket %s: %w", path, err)
	}
	if err := privateDir(dir, uid); err != nil {
		return nil, fmt.Errorf("unix socket %s: %w", path, err)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("unix socket %s: the path exists and is not a socket", path)
		}
		if c, err := net.DialTimeout(NetworkUnix, path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("unix socket %s: another process is serving it", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("unix socket %s: removing the stale socket: %w", path, err)
		}
	}
	ln, err := net.Listen(NetworkUnix, path)
	if err != nil {
		return nil, fmt.Errorf("unix socket %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("unix socket %s: %w", path, err)
	}
	return &peerListener{Listener: ln, uid: uid, path: path, log: logger}, nil
}

// privateDir refuses a socket directory that another user could replace the socket in: one not
// owned by uid, writable by group or other, or a symlink (whoever can write its parent can repoint
// it). Its ancestors are not checked.
func privateDir(dir string, uid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory %s is a symlink: want the directory itself", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if int(st.Uid) != uid || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("directory %s is mode %04o owned by uid %d: want one owned by uid %d and not writable by group or other", dir, fi.Mode().Perm(), st.Uid, uid)
	}
	return nil
}

// peerListener is a Unix listener that closes, and logs, every connection whose peer uid is not uid.
type peerListener struct {
	net.Listener
	uid  int
	path string
	log  *log.Logger
}

// Accept returns the next connection from the admitted uid; the refused ones never reach the caller.
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			c.Close()
			return nil, errors.New("unix socket: accepted a connection that is not a Unix one")
		}
		uid, err := peerUID(uc)
		switch {
		case err != nil:
			l.log.Printf("listen: refused a connection on %s: reading the peer uid: %v", l.path, err)
		case uid != l.uid:
			l.log.Printf("listen: refused a connection on %s from uid %d (admits uid %d)", l.path, uid, l.uid)
		default:
			return uc, nil
		}
		uc.Close()
	}
}

type unixConnKey struct{}

// ConnContext is http.Server.ConnContext for the listeners Listen makes: a connection accepted on a
// Unix socket marks its requests' context, which ViaUnixSocket reads.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if _, ok := c.(*net.UnixConn); ok {
		return context.WithValue(ctx, unixConnKey{}, true)
	}
	return ctx
}

// ViaUnixSocket reports whether the request ctx belongs to arrived on a Unix socket (ConnContext).
func ViaUnixSocket(ctx context.Context) bool {
	v, _ := ctx.Value(unixConnKey{}).(bool)
	return v
}
