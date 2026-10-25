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

// serve -h and doctor -h print every variable with its default and the config-file rule, and exit 0.
func TestServeAndDoctorHelpListEveryVariable(t *testing.T) {
	for _, cmd := range []string{"serve", "doctor"} {
		code, _, errs := run(cmd, "-h")
		if code != 0 || !strings.Contains(errs, "usage: batondeck-kanban "+cmd) || !strings.Contains(errs, "environment > config file > defaults") {
			t.Fatalf("%s -h: %d %q", cmd, code, errs)
		}
		for _, v := range core.EnvVars() {
			if !strings.Contains(errs, "  "+v.Name+"\n") || !strings.Contains(errs, "default: "+v.Default+"\n") {
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
