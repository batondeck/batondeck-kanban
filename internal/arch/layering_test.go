// Package arch holds the layering guard of ADR-0003: the import direction between the packages of
// this module, asserted from one `go list -json` so a package is judged the moment it exists.
package arch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/batondeck/batondeck-kanban"

type kind int

const (
	direct     kind = iota // checked over .Imports
	transitive             // checked over the whole import graph `go list -deps` returns
)

// rule is one row of ADR-0003 §C. Paths are module-relative when they start with "internal/",
// "cmd", "contract" or "migrations", and import paths otherwise; every path matches itself and
// everything below it.
type rule struct {
	row       int
	kind      kind
	subjects  []string // "*" is every package of the module
	except    []string // subjects the row exempts
	forbidden []string
	peers     bool     // the subjects may not import one another (row 6 "each other")
	through   []string // a transitive chain entering one of these is not followed (row 2)
}

var drivers = []string{"database/sql", "modernc.org/sqlite", "github.com/jackc/pgx", "github.com/pressly/goose"}

// layeringRules is ADR-0003 §C, row for row.
var layeringRules = []rule{
	// §C row 1: internal/domain and internal/core reach no internal package.
	{row: 1, kind: transitive, subjects: []string{"internal/domain", "internal/core"}, forbidden: []string{"internal"}},
	// §C row 2, amended by the orchestrator 2026-10-23 (through-store reach allowed): outside the
	// store, migrations and the composition root, no import chain reaches a database driver except
	// one passing through internal/store/... or migrations.
	{row: 2, kind: transitive, subjects: []string{"*"}, except: []string{"internal/store", "migrations", "internal/cli", "cmd"}, forbidden: drivers, through: []string{"internal/store", "migrations"}},
	// §C row 3: the store imports no layer above it.
	{row: 3, kind: direct, subjects: []string{"internal/store"}, forbidden: []string{"internal/service", "internal/mcp", "internal/api", "internal/auth", "internal/bus", "internal/wake", "internal/cli"}},
	// §C row 4: bus, retention, auth and blob import no service, transport or root.
	{row: 4, kind: direct, subjects: []string{"internal/bus", "internal/retention", "internal/auth", "internal/blob"}, forbidden: []string{"internal/service", "internal/mcp", "internal/api", "internal/wake", "internal/cli"}},
	// §C row 5: the service imports no auth, transport or root.
	{row: 5, kind: direct, subjects: []string{"internal/service"}, forbidden: []string{"internal/mcp", "internal/api", "internal/auth", "internal/wake", "internal/cli"}},
	// §C row 6: a transport imports no store package, no root and no other transport.
	{row: 6, kind: direct, subjects: []string{"internal/mcp", "internal/api", "internal/wake"}, forbidden: []string{"internal/store", "internal/cli"}, peers: true},
	// §C row 7: only internal/mcp and the composition root import the MCP SDK.
	{row: 7, kind: direct, subjects: []string{"*"}, except: []string{"internal/mcp", "internal/cli"}, forbidden: []string{"github.com/modelcontextprotocol/go-sdk"}},
	// §C row 8: contract/ non-test code reaches no internal package.
	{row: 8, kind: transitive, subjects: []string{"contract"}, forbidden: []string{"internal"}},
}

// testRules is the looser table §C applies to TestImports and XTestImports: rows 1 and 3 to 7,
// direct, with the fakes allowed. Rows 2 and 8 do not bind tests (row 8 says non-test; a test at
// the join opens both engines).
var testRules = []rule{layeringRules[0], layeringRules[2], layeringRules[3], layeringRules[4], layeringRules[5], layeringRules[6]}

// testFakes are the packages any test may import whatever its layer (ADR-0003 B2, B3, B4).
var testFakes = []string{"internal/store/memstore", "internal/service/servicefake", "internal/auth/authfake"}

type pkg struct {
	ImportPath   string
	Imports      []string
	DepOnly      bool
	TestImports  []string
	XTestImports []string
	Error        *struct{ Err string }
}

// full turns a module-relative path into an import path.
func full(p string) string {
	for _, root := range []string{"internal", "cmd", "contract", "migrations", "web"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return module + "/" + p
		}
	}
	return p
}

func short(p string) string { return strings.TrimPrefix(p, module+"/") }

func under(path, prefix string) bool {
	prefix = full(prefix)
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func underAny(path string, prefixes []string) (string, bool) {
	for _, p := range prefixes {
		if under(path, p) {
			return p, true
		}
	}
	return "", false
}

// subject returns the subject entry of r that path falls under.
func (r rule) subject(path string) (string, bool) {
	if !strings.HasPrefix(path, module+"/") {
		return "", false
	}
	if _, ex := underAny(path, r.except); ex {
		return "", false
	}
	if slices.Contains(r.subjects, "*") {
		return "*", true
	}
	return underAny(path, r.subjects)
}

// forbids reports whether r refuses an edge from a package under subject s to target. An edge that
// stays inside its own subject (internal/mcp/tools → internal/mcp) is never refused.
func (r rule) forbids(s, target string) bool {
	if s != "*" && under(target, s) {
		return false
	}
	if _, ok := underAny(target, r.forbidden); ok {
		return true
	}
	if r.peers {
		_, ok := underAny(target, r.subjects)
		return ok
	}
	return false
}

// moduleRoot is two directories up from this package, checked by its go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("no go.mod at the module root: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "module "+module {
		t.Fatalf("%s/go.mod does not declare module %s", root, module)
	}
	return root
}

// packageDirs lists every directory of the module holding a .go file, as ./dir patterns. The walk
// never enters node_modules, testdata, a dot or underscore directory or a nested module, so a
// web/node_modules full of npm packages that ship .go files is never handed to go list.
func packageDirs(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if p != root {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			rel, err := filepath.Rel(root, filepath.Dir(p))
			if err != nil {
				return err
			}
			seen["./"+filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

func loadPackages(t *testing.T) []pkg {
	t.Helper()
	root := moduleRoot(t)
	cmd := exec.Command("go", append([]string{"list", "-e", "-deps", "-json"}, packageDirs(t, root)...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		if p.Error != nil {
			t.Errorf("go list could not load %s, so its edges are unjudged: %s", short(p.ImportPath), p.Error.Err)
		}
		pkgs = append(pkgs, p)
	}
	// A guard that loaded nothing passes everything; these three exist on main.
	for _, want := range []string{"internal/domain", "internal/store", "internal/cli"} {
		if !slices.ContainsFunc(pkgs, func(p pkg) bool { return !p.DepOnly && p.ImportPath == full(want) }) {
			t.Fatalf("go list returned no %s; the guard is not looking at this module", want)
		}
	}
	return pkgs
}

// reach walks the import graph from `from` breadth first and returns, per forbidden entry of r,
// the shortest chain to a package r refuses. It does not walk past a refused package, nor into
// r.through.
func reach(byPath map[string]pkg, r rule, s, from string) []string {
	prev := map[string]string{from: ""}
	path := func(n string) string {
		var hops []string
		for ; n != ""; n = prev[n] {
			hops = append([]string{short(n)}, hops...)
		}
		return strings.Join(hops, " → ")
	}
	var found []string
	reported := map[string]bool{}
	for queue := []string{from}; len(queue) > 0; queue = queue[1:] {
		for _, imp := range byPath[queue[0]].Imports {
			if _, seen := prev[imp]; seen {
				continue
			}
			prev[imp] = queue[0]
			if r.forbids(s, imp) {
				entry, ok := underAny(imp, r.forbidden)
				if !ok {
					entry = imp
				}
				if !reported[entry] {
					reported[entry] = true
					found = append(found, path(imp))
				}
				continue
			}
			if _, stop := underAny(imp, r.through); stop {
				continue
			}
			queue = append(queue, imp)
		}
	}
	return found
}

func TestLayering(t *testing.T) {
	pkgs := loadPackages(t)
	byPath := map[string]pkg{}
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
	}
	for _, p := range pkgs {
		if p.DepOnly {
			continue // a dependency go list -deps returned, not a package of this module
		}
		for _, r := range layeringRules {
			s, ok := r.subject(p.ImportPath)
			if !ok {
				continue
			}
			if r.kind == transitive {
				for _, c := range reach(byPath, r, s, p.ImportPath) {
					t.Errorf("rule %d (transitive): %s", r.row, c)
				}
				continue
			}
			for _, target := range p.Imports {
				if r.forbids(s, target) {
					t.Errorf("rule %d (direct): %s → %s", r.row, short(p.ImportPath), short(target))
				}
			}
		}
		testImports := append(slices.Clone(p.TestImports), p.XTestImports...)
		for _, r := range testRules {
			s, ok := r.subject(p.ImportPath)
			if !ok {
				continue
			}
			for _, target := range testImports {
				if target == p.ImportPath {
					continue
				}
				if _, fake := underAny(target, testFakes); fake {
					continue
				}
				if r.forbids(s, target) {
					t.Errorf("rule %d (test, direct): %s's tests → %s", r.row, short(p.ImportPath), short(target))
				}
			}
		}
	}
}
