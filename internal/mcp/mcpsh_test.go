package mcp

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// plugin/scripts/mcp.sh, the plugin's one-call shell client, ends the MCP session it opens whether
// its call succeeds or fails (a leaked session held a slot of the server's bound for its 30 min idle
// window), and a server it cannot reach is exit 1, as its header promises, not curl's own status.
func TestMCPShEndsItsSession(t *testing.T) {
	for _, tool := range []string{"bash", "curl", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "plugin", "scripts", "mcp.sh"))
	if err != nil {
		t.Fatal(err)
	}
	h := bootHarness(t, servicefake.New().Services(), resolver())
	plant(h)
	run := func(base, tool, args string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", script, tool, args)
		cmd.Env = append(os.Environ(), "BATONDECK_URL="+base, "BATONDECK_TOKEN="+tokenAda, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			return string(out), exit.ExitCode()
		case err != nil:
			t.Fatalf("running mcp.sh: %v", err)
		}
		return string(out), 0
	}
	live := func() int {
		h.srv.sessions.mu.Lock()
		defer h.srv.sessions.mu.Unlock()
		return len(h.srv.sessions.live)
	}
	base := strings.TrimSuffix(h.url, "/mcp")

	if out, code := run(base, "whoami", `{"projectId":"P-1"}`); code != 0 || !strings.Contains(out, "u-ada") {
		t.Fatalf("mcp.sh whoami: exit %d, %q", code, out)
	}
	waitFor(t, func() bool { return live() == 0 })

	if out, code := run(base, "stale", `{"projectId":"P-1"}`); code != 1 || !strings.Contains(out, "TOOL_ERROR") {
		t.Fatalf("mcp.sh stale: exit %d, %q, want 1 and the tool's error", code, out)
	}
	waitFor(t, func() bool { return live() == 0 })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := "http://" + ln.Addr().String()
	_ = ln.Close()
	if out, code := run(down, "whoami", `{}`); code != 1 || !strings.HasPrefix(out, "mcp.sh: cannot reach ") || strings.Count(out, "\n") != 1 {
		t.Errorf("mcp.sh against no server: exit %d, %q, want 1 and its own one line (no curl line)", code, out)
	}
}
