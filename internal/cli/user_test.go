package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// withStdin feeds the commands' password reader from a pipe holding input.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := stdin
	stdin = r
	t.Cleanup(func() { stdin = old; r.Close() })
}

// migratedDB points the config at a fresh, migrated SQLite file.
func migratedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bk.db")
	t.Setenv("BATONDECK_DATABASE_URL", path)
	if code, _, errs := run("migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, errs)
	}
	return path
}

func TestUserCommandsAddListPasswdDisable(t *testing.T) {
	migratedDB(t)
	withStdin(t, "s3cret-s3cret\n")
	code, out, errs := run("user", "add", "--name", "Alice A", "Alice@Example.test")
	if code != 0 || !regexp.MustCompile(`^user add: U-[0-9a-f]{16} alice@example\.test\n$`).MatchString(out) {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	withStdin(t, "again-again-again\n")
	if code, _, errs := run("user", "add", "alice@example.test"); code != 1 || !strings.Contains(errs, "a user with that login exists") {
		t.Fatalf("duplicate: %d %q", code, errs)
	}
	withStdin(t, "new-password-1\n")
	if code, out, errs := run("user", "passwd", "alice@example.test"); code != 0 || out != "user passwd: password set for alice@example.test\n" {
		t.Fatalf("passwd: %d %q %q", code, out, errs)
	}
	if code, _, errs := run("user", "disable", "nobody"); code != 1 || !strings.Contains(errs, "NOT_FOUND") {
		t.Fatalf("disable nobody: %d %q", code, errs)
	}
	if code, out, _ := run("user", "disable", "alice@example.test"); code != 0 || !strings.Contains(out, " disabled at ") {
		t.Fatalf("disable: %d %q", code, out)
	}
	code, out, _ = run("user", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[1], "Alice A") || strings.HasSuffix(lines[1], "-") {
		t.Fatalf("list: %d %q", code, out)
	}
	if strings.Contains(out, "argon2") || strings.Contains(out, "s3cret-s3cret") {
		t.Fatal("user list prints the hash or the password")
	}
}

func TestUserCommandsRefuseAnUnmigratedStoreAndATerminal(t *testing.T) {
	t.Setenv("BATONDECK_DATABASE_URL", filepath.Join(t.TempDir(), "missing.db"))
	if code, _, errs := run("user", "list"); code != 1 || !strings.Contains(errs, "run `batondeck-kanban migrate` first") {
		t.Fatalf("missing store: %d %q", code, errs)
	}
	migratedDB(t)
	tty, err := os.Open("/dev/null") // a character device, as a terminal is
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	old := stdin
	stdin = tty
	defer func() { stdin = old }()
	if code, _, errs := run("user", "add", "bob"); code != 1 || !strings.Contains(errs, "would echo it") {
		t.Fatalf("terminal: %d %q", code, errs)
	}
}

// user add and user passwd refuse a password under 12 characters, saying so; 12 is accepted.
func TestUserCommandsRefuseAShortPassword(t *testing.T) {
	migratedDB(t)
	for _, args := range [][]string{{"user", "add", "alice2"}, {"user", "passwd", "alice2"}} {
		withStdin(t, "elevenchars\n")
		if code, _, errs := run(args...); code != 1 || !strings.Contains(errs, "the password is 11 characters: it must be at least 12") {
			t.Fatalf("%v: %d %q", args, code, errs)
		}
	}
	withStdin(t, "twelve-chars\n")
	if code, _, errs := run("user", "add", "alice2"); code != 0 {
		t.Fatalf("12 characters: %d %q", code, errs)
	}
	withStdin(t, "twelve-chars\n")
	if code, _, errs := run("user", "passwd", "alice2"); code != 0 {
		t.Fatalf("passwd 12 characters: %d %q", code, errs)
	}
}
