package wake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
)

// The Claude Code adapter (R3.1, docs/release/socket-wake-spike-2026-10.md): the registry entry
// Claude Code keeps per session process, and the one NDJSON line written into its inbox socket.

// AdapterClaudeCode is the one client whose socket the dispatcher can write.
const AdapterClaudeCode = "claude-code"

// peerProtocol is the registry's peerProtocol the frame below was proven on (Claude Code 2.1.309).
const peerProtocol = 1

// registryEntry is what the dispatcher reads of ~/.claude/sessions/<pid>.json.
type registryEntry struct {
	PID                 int64  `json:"pid"`
	SessionID           string `json:"sessionId"`
	ProcStart           string `json:"procStart"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	PeerProtocol        int    `json:"peerProtocol"`
}

// errNoRegistry is a pid with no registry entry: not a Claude Code session, or one that exited.
var errNoRegistry = errors.New("no Claude Code registry entry")

// readRegistry is pid's registry entry in dir, read now: the sessionId changes under /clear, so it
// is read at every delivery rather than kept.
func readRegistry(dir string, pid int64) (registryEntry, error) {
	b, err := os.ReadFile(filepath.Join(dir, strconv.FormatInt(pid, 10)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return registryEntry{}, errNoRegistry
	}
	if err != nil {
		return registryEntry{}, err
	}
	var r registryEntry
	if err := json.Unmarshal(b, &r); err != nil {
		return registryEntry{}, fmt.Errorf("the registry entry of pid %d: %w", pid, err)
	}
	if r.PID != pid || r.SessionID == "" || r.MessagingSocketPath == "" {
		return registryEntry{}, fmt.Errorf("the registry entry of pid %d names pid %d, session %q, socket %q", pid, r.PID, r.SessionID, r.MessagingSocketPath)
	}
	return r, nil
}

// Session is the registry entry of one live Claude Code process, as `session register` sends it.
type Session struct {
	PID       int64
	SessionID string
	ProcStart string
}

// SessionOf is the one live process in dir whose registry entry names socket as its
// messagingSocketPath (the R3.1 spike's way to find the hook's Claude Code process), read with the
// parser the directory validates a registration against.
func SessionOf(dir, socket string) (Session, error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		return Session{}, err
	}
	var found []Session
	for _, n := range names {
		pid, err := strconv.ParseInt(strings.TrimSuffix(n.Name(), ".json"), 10, 64)
		if err != nil || !strings.HasSuffix(n.Name(), ".json") || pid <= 0 {
			continue
		}
		r, err := readRegistry(dir, pid)
		if err != nil || r.MessagingSocketPath != socket {
			continue
		}
		if _, err := core.ProcessUID(int(pid)); err != nil {
			continue
		}
		found = append(found, Session{PID: pid, SessionID: r.SessionID, ProcStart: r.ProcStart})
	}
	switch len(found) {
	case 0:
		return Session{}, fmt.Errorf("no live session in %s names the socket %s", dir, socket)
	case 1:
		return found[0], nil
	}
	return Session{}, fmt.Errorf("%d live sessions in %s name the socket %s", len(found), dir, socket)
}

// peerToken is the session's peer token from <pid>.<sha256(socket)>.key beside its registry entry,
// "" when there is none. It is sent as the optional auth line and never logged.
func peerToken(dir string, pid int64, socket string) string {
	h := sha256.Sum256([]byte(socket))
	b, err := os.ReadFile(filepath.Join(dir, strconv.FormatInt(pid, 10)+"."+hex.EncodeToString(h[:])+".key"))
	if err != nil {
		return ""
	}
	var k struct {
		PeerToken string `json:"peerToken"`
	}
	_ = json.Unmarshal(b, &k)
	return k.PeerToken
}

// frameBytes is the bytes one doorbell writes: the auth line when there is a token, then one user
// frame naming the receiver's session. No priority, no from, never a from-mode attestation (R3.1
// §4 items 1 and 3).
func frameBytes(sessionID, token string, f Frame) []byte {
	var out []byte
	if token != "" {
		b, _ := json.Marshal(struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}{"auth", token})
		out = append(append(out, b...), '\n')
	}
	b, _ := json.Marshal(struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Message   struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user", SessionID: sessionID, Message: struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: f.Line()}})
	return append(append(out, b...), '\n')
}

// writeDoorbell opens one connection to socket, writes the doorbell and closes it; nothing comes
// back. Each step is bounded by timeout.
func writeDoorbell(socket string, b []byte, timeout time.Duration) error {
	c, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, err = c.Write(b)
	return err
}

// gone reports a delivery failure that means the client is no longer there: the socket file is
// missing, or nothing listens on it (a SIGKILL leaves the file).
func gone(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
