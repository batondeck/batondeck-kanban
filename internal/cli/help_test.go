package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/core"
)

// TestMain points the default data dir at a temporary one, so no test writes the developer's own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bk-cli-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_DATA_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// clientOnlyEnv is read by a client command (session), not the server, so serve -h does not list it.
var clientOnlyEnv = []string{"BATONDECK_TOKEN"}

// Every BATONDECK_* variable a server source reads is in core.EnvVars, and every one listed is
// read somewhere: the help cannot drift from the code in either direction.
func TestEnvVarsListEveryVariableTheSourcesRead(t *testing.T) {
	re := regexp.MustCompile(`"(BATONDECK_[A-Z0-9_]+)"`)
	read := map[string]bool{}
	for _, glob := range []string{"../core/*.go", "*.go"} {
		files, err := filepath.Glob(glob)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %v", glob, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				if !slices.Contains(clientOnlyEnv, m[1]) {
					read[m[1]] = true
				}
			}
		}
	}
	listed := map[string]bool{}
	for _, v := range core.EnvVars() {
		listed[v.Name] = true
		if v.Default == "" || v.Doc == "" {
			t.Errorf("%s has no default or no description", v.Name)
		}
		if !read[v.Name] {
			t.Errorf("%s is listed but no source reads it", v.Name)
		}
	}
	for name := range read {
		if !listed[name] {
			t.Errorf("%s is read but core.EnvVars does not list it, so serve -h and doctor -h do not", name)
		}
	}
}

// serve -h and doctor -h print every variable with its default and the config-file rule on stdout,
// and exit 0.
func TestServeAndDoctorHelpListEveryVariable(t *testing.T) {
	for _, cmd := range []string{"serve", "doctor"} {
		code, out, errs := run(cmd, "-h")
		if code != 0 || errs != "" || !strings.Contains(out, "usage: batondeck-kanban "+cmd) || !strings.Contains(out, "environment > config file > defaults") {
			t.Fatalf("%s -h: %d %q %q", cmd, code, out, errs)
		}
		for _, v := range core.EnvVars() {
			if !strings.Contains(out, "  "+v.Name+"\n") || !strings.Contains(out, "default: "+v.Default+"\n") {
				t.Errorf("%s -h does not list %s with its default", cmd, v.Name)
			}
		}
	}
}

// help, -h and --help print the usage to stdout and exit 0; an unknown command still exits 2.
func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		if code, out, errs := run(arg); code != 0 || !strings.HasPrefix(out, "usage: batondeck-kanban <command>") || errs != "" {
			t.Fatalf("%s: %d %q %q", arg, code, out, errs)
		}
	}
	if code, _, errs := run("bogus"); code != 2 || !strings.Contains(errs, `unknown command "bogus"`) {
		t.Fatalf("bogus: %d %q", code, errs)
	}
}

// P-311: user, token and session answer help, -h and --help in a subcommand's place with their
// usage on stdout and 0, as the top level does; a bare group or an unknown subcommand is still a
// usage error on stderr.
func TestSubcommandHelpExitsZero(t *testing.T) {
	for _, group := range []string{"user", "token", "session"} {
		for _, arg := range []string{"help", "-h", "--help"} {
			code, out, errs := run(group, arg)
			if code != 0 || !strings.HasPrefix(out, "usage: batondeck-kanban "+group+" <subcommand>") || errs != "" {
				t.Errorf("%s %s: %d %q %q", group, arg, code, out, errs)
			}
		}
		for _, args := range [][]string{{group}, {group, "frob"}} {
			code, out, errs := run(args...)
			if code != 2 || out != "" || !strings.Contains(errs, "usage: batondeck-kanban "+group) {
				t.Errorf("%v: %d %q %q, want 2 and the usage on stderr", args, code, out, errs)
			}
		}
	}
}

// P-311: every command's and subcommand's own -h and --help print its flags on stdout and exit 0;
// a flag that does not parse is still the error and the flags on stderr, exit 2.
func TestEveryFlagSetHelpExitsZero(t *testing.T) {
	commands := [][]string{{"serve"}, {"migrate"}, {"doctor"}, {"healthcheck"},
		{"user", "add"}, {"user", "disable"}, {"user", "enable"}, {"user", "passwd"}, {"user", "list"},
		{"token", "create"}, {"token", "list"}, {"token", "revoke"},
		{"session", "register"}, {"session", "unregister"}}
	for _, cmd := range commands {
		for _, flag := range []string{"-h", "--help"} {
			code, out, errs := run(append(slices.Clone(cmd), flag)...)
			if code != 0 || !strings.Contains(out, "-config") || errs != "" {
				t.Errorf("%v %s: %d %q %q, want 0 and the flags on stdout", cmd, flag, code, out, errs)
			}
		}
		code, out, errs := run(append(slices.Clone(cmd), "-bogus")...)
		if code != 2 || out != "" || !strings.Contains(errs, "flag provided but not defined: -bogus") || !strings.Contains(errs, "-config") {
			t.Errorf("%v -bogus: %d %q %q, want 2 and the error on stderr", cmd, code, out, errs)
		}
	}
}
