package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

func sessionUsage(w io.Writer) {
	fmt.Fprint(w, `usage: batondeck-kanban session <subcommand> [flags]

  register --role worker|master [--agent LABEL]   add this session to the server's wake directory
  unregister                                      remove it

Both run from a client's SessionStart and SessionEnd hooks. They reach the server only on the
first unix:// entry of its listen config, with the bearer in BATONDECK_TOKEN. The session is the
Claude Code process whose registry entry names $CLAUDE_CODE_MESSAGING_SOCKET, else the parent
process. --agent takes only a label the bearer may claim for: its token's own label, or a name no
other account's token and no live token of its own carries (its own revoked or expired label
included). Another account's label is refused FORBIDDEN (revoked or expired tokens included), so
is a live label of the bearer's own account named from a token that does not carry it, and so is
id:<identity> for any identity but the bearer's own. A failure is one line on stderr and exit 0,
so a hook never breaks a session start.
every subcommand takes -config PATH.
`)
}

// sessionTimeout bounds one request: a hook waits on it before the session starts.
const sessionTimeout = 3 * time.Second

// session is `batondeck-kanban session register|unregister`. Usage errors exit 2; every other
// outcome exits 0, a failure as exactly one stderr line, and nothing is written to stdout (a
// SessionStart hook's stdout becomes the model's context) but the usage a person asked for with
// help, -h or --help, which no hook passes.
func session(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		sessionUsage(stderr)
		return 2
	}
	if isHelp(args[0]) {
		sessionUsage(stdout)
		return 0
	}
	sub, rest := args[0], args[1:]
	var cfgPath, role, agent string
	fs := commonFlags("session "+sub, &cfgPath, stderr)
	switch sub {
	case "register":
		fs.StringVar(&role, "role", "", "worker or master (required)")
		fs.StringVar(&agent, "agent", "", "the agent label this session works as")
	case "unregister":
	default:
		fmt.Fprintf(stderr, "session: unknown subcommand %q\n", sub)
		sessionUsage(stderr)
		return 2
	}
	if code, ok := parseFlags(fs, rest, stdout); !ok {
		return code
	}
	if fs.NArg() != 0 || (sub == "register" && role != "worker" && role != "master") {
		sessionUsage(stderr)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "session %s: %s\n", sub, strings.ReplaceAll(fmt.Sprintf(format, a...), "\n", " "))
		return 0
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	sock := ""
	for _, e := range cfg.Listen {
		if a, err := core.ParseListen(e); err == nil && a.Network == core.NetworkUnix {
			sock = a.Address
			break
		}
	}
	if sock == "" {
		return fail("no unix:// entry in listen %v; the server takes sessions only on its Unix socket", cfg.Listen)
	}
	bearer := os.Getenv("BATONDECK_TOKEN")
	if bearer == "" {
		return fail("BATONDECK_TOKEN is not set")
	}
	me, socket, err := thisSession()
	if err != nil {
		return fail("%v", err)
	}
	client := &http.Client{Timeout: sessionTimeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, core.NetworkUnix, sock)
		}}}
	var req *http.Request
	if sub == "register" {
		body, _ := json.Marshal(struct {
			PID       int64  `json:"pid"`
			Role      string `json:"role"`
			Agent     string `json:"agent,omitempty"`
			Socket    string `json:"socket,omitempty"`
			SessionID string `json:"sessionId,omitempty"`
			ProcStart string `json:"procStart,omitempty"`
		}{me.PID, role, agent, socket, me.SessionID, me.ProcStart})
		req, _ = http.NewRequest(http.MethodPost, "http://batondeck-kanban/internal/wake-session", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("http://batondeck-kanban/internal/wake-session/%d", me.PID), nil)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := client.Do(req)
	if err != nil {
		return fail("pid %d: the server on %s: %v", me.PID, sock, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	want := http.StatusCreated
	if sub == "unregister" {
		want = http.StatusNoContent
	}
	if resp.StatusCode != want {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
		}
		return fail("pid %d: refused, %s %s: %s", me.PID, resp.Status, e.Code, e.Message)
	}
	if sub == "unregister" {
		fmt.Fprintf(stderr, "session unregister: pid %d removed\n", me.PID)
		return 0
	}
	var got wake.RegisterResp
	_ = json.Unmarshal(raw, &got)
	fmt.Fprintf(stderr, "session register: pid %d registered as %s, agent %s, adapter %q, entry %s\n", got.PID, got.Role, got.AgentID, got.Adapter, got.ID)
	return 0
}

// thisSession is the client process a hook registers and the messaging socket it names: with
// CLAUDE_CODE_MESSAGING_SOCKET set, the live Claude Code process whose registry entry names that
// socket (the hook's own parent may be a shell Claude Code started); without it, the parent process.
func thisSession() (wake.Session, string, error) {
	socket := os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if socket == "" {
		return wake.Session{PID: int64(os.Getppid())}, "", nil
	}
	s, err := wake.SessionOf(wakeRegistry(), socket)
	return s, socket, err
}
