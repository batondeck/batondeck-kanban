package cli

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// resolve runs ResolveCaller against the file the commands wrote.
func resolve(t *testing.T, path, bearer string) (domain.Caller, error) {
	t.Helper()
	st, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st}, Sessions: st}
	return svc.ResolveCaller(context.Background(), bearer)
}

// AC1: user add, then token create prints the token once; token list shows its prefix and never
// the plaintext; token revoke makes ResolveCaller answer UNAUTHENTICATED.
func TestTokenCreateListRevoke(t *testing.T) {
	path := migratedDB(t)
	withStdin(t, "pw-twelve-chars\n")
	if code, _, errs := run("user", "add", "alice"); code != 0 {
		t.Fatalf("user add: %s", errs)
	}
	code, out, errs := run("token", "create", "--user", "alice")
	if code != 0 || !regexp.MustCompile(`^bd_[0-9a-f]{16}_[A-Za-z0-9_-]{43}\n$`).MatchString(out) {
		t.Fatalf("create: %d %q %q", code, out, errs)
	}
	plain := strings.TrimSpace(out)
	prefix := plain[:3+16]
	if !strings.Contains(errs, prefix) || !strings.Contains(errs, "only time the token is shown") || strings.Contains(errs, plain) {
		t.Fatalf("create's note: %q", errs)
	}
	c, err := resolve(t, path, plain)
	if err != nil || c.TokenScope != c.Identity || !strings.HasPrefix(c.Identity, "U-") {
		t.Fatalf("a live token: %+v %v", c, err)
	}
	code, out, _ = run("token", "list")
	if code != 0 || !strings.Contains(out, prefix) || !strings.Contains(out, "alice") || !strings.Contains(out, " live") || strings.Contains(out, plain[20:]) {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, errs = run("token", "revoke", plain)
	if code != 1 || !strings.Contains(errs, "VALIDATION") || strings.Count(out+errs, plain[20:]) != 0 {
		t.Fatalf("revoke with the whole token: %d, the secret appears %d times in %q", code, strings.Count(out+errs, plain[20:]), out+errs)
	}
	if code, out, errs := run("token", "revoke", prefix); code != 0 || !strings.HasPrefix(out, "token revoke: "+prefix+" revoked at ") {
		t.Fatalf("revoke: %d %q %q", code, out, errs)
	}
	_, err = resolve(t, path, plain)
	if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeUnauthenticated {
		t.Fatalf("a revoked token: %v", err)
	}
	if _, out, _ := run("token", "list", "--user", "alice"); !strings.Contains(out, " revoked") {
		t.Fatalf("list after revoke: %q", out)
	}
	if code, _, errs := run("token", "revoke", "bd_0000000000000000"); code != 1 || !strings.Contains(errs, "NOT_FOUND") {
		t.Fatalf("revoke a missing token: %d %q", code, errs)
	}
	if code, _, errs := run("token", "create", "--user", "nobody"); code != 1 || !strings.Contains(errs, "NOT_FOUND") {
		t.Fatalf("create for nobody: %d %q", code, errs)
	}
	if code, _, _ := run("user", "disable", "alice"); code != 0 {
		t.Fatal("disable")
	}
	if code, _, errs := run("token", "create", "--user", "alice"); code != 1 || !strings.Contains(errs, "disabled") {
		t.Fatalf("create for a disabled user: %d %q", code, errs)
	}
}

// AC3: `user add --admin` sets the workspace-admin flag, `user add` without it does not; both are
// read back by user list and by ResolveCaller.
func TestUserAddAdminFlag(t *testing.T) {
	path := migratedDB(t)
	withStdin(t, "pw-twelve-chars\n")
	if code, out, errs := run("user", "add", "--admin", "root"); code != 0 || !strings.HasSuffix(out, " root (workspace admin)\n") {
		t.Fatalf("add --admin: %d %q %q", code, out, errs)
	}
	withStdin(t, "pw-twelve-chars\n")
	if code, out, errs := run("user", "add", "alice"); code != 0 || strings.Contains(out, "admin") {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	_, out, _ := run("user", "list")
	if !regexp.MustCompile(`(?m)^U-\S+\s+root\s+root\s+true\s`).MatchString(out) || !regexp.MustCompile(`(?m)^U-\S+\s+alice\s+alice\s+false\s`).MatchString(out) {
		t.Fatalf("list: %q", out)
	}
	for login, want := range map[string]bool{"root": true, "alice": false} {
		_, tok, errs := run("token", "create", "--user", login)
		c, err := resolve(t, path, strings.TrimSpace(tok))
		if err != nil || c.WorkspaceAdmin != want {
			t.Fatalf("%s: %+v %v %q, want admin %v", login, c, err, errs, want)
		}
	}
}

// P-159: `token create` and `token revoke` each write their audit row, actor host, via cli,
// subject the user the token acts for; a refused revoke writes none.
func TestTokenSubcommandIsAudited(t *testing.T) {
	path := migratedDB(t)
	withStdin(t, "pw-twelve-chars\n")
	if code, _, errs := run("user", "add", "alice"); code != 0 {
		t.Fatalf("user add: %s", errs)
	}
	_, out, _ := run("token", "create", "--user", "alice", "--label", "ci")
	id := strings.TrimSpace(out)[3 : 3+16]
	run("token", "revoke", "bd_0000000000000000")
	if code, _, errs := run("token", "revoke", "bd_"+id); code != 0 {
		t.Fatalf("revoke: %s", errs)
	}
	st, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	page, err := st.ListAuditLog(context.Background(), store.AuditFilter{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range page.Rows {
		got = append(got, r.Action+" "+r.Actor+">"+strings.SplitN(r.Subject, "-", 2)[0]+" "+string(r.Data))
	}
	want := `cli_token.revoked host>U {"label":"ci","tokenId":"` + id + `","via":"cli"}` + "\n" +
		`cli_token.minted host>U {"label":"ci","tokenId":"` + id + `","via":"cli"}`
	if strings.Join(got, "\n") != want {
		t.Fatalf("audit rows:\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

// P-257 B1 (review): the host's token create refuses a label another user's live token carries.
func TestTokenCreateRefusesAnotherUsersLabel(t *testing.T) {
	migratedDB(t)
	for _, login := range []string{"alice", "bob"} {
		withStdin(t, "pw-twelve-chars\n")
		if code, _, errs := run("user", "add", login); code != 0 {
			t.Fatalf("user add %s: %s", login, errs)
		}
	}
	if code, _, errs := run("token", "create", "--user", "alice", "--label", "agent-a"); code != 0 {
		t.Fatalf("alice's agent-a: %s", errs)
	}
	code, out, errs := run("token", "create", "--user", "bob", "--label", "agent-a")
	if code != 1 || out != "" || !strings.Contains(errs, "VALIDATION") || !strings.Contains(errs, `label "agent-a" is another account's agent`) {
		t.Fatalf("bob's agent-a: %d %q %q, want exit 1 and the VALIDATION refusal", code, out, errs)
	}
}
