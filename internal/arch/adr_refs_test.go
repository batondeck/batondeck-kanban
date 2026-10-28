package arch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// adrDir is where the decision records live in this repository: docs/adr/NNNN-slug.md, vendored
// from the planning workspace so that a citation in a public tree resolves (audit D4).
const adrDir = "docs/adr"

var (
	adrFileName = regexp.MustCompile(`^([0-9]{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	// adrCitation is an ADR id as the tree cites it. The trailing group refuses a fifth digit, so
	// the id is exactly four.
	adrCitation = regexp.MustCompile(`ADR-([0-9]{4})(?:[^0-9]|$)`)
)

// adrIDs reads docs/adr and returns the ids that have a file, refusing a file whose name looks
// numbered but is not NNNN-slug.md, and a record whose heading names a different id.
func adrIDs(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, adrDir))
	if err != nil {
		t.Fatalf("reading %s: %v", adrDir, err)
	}
	ids := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		m := adrFileName.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("%s/%s: a decision record is named NNNN-slug.md (lowercase slug)", adrDir, name)
			continue
		}
		id := "ADR-" + m[1]
		if prev, dup := ids[id]; dup {
			t.Errorf("%s: two files for %s: %s and %s", adrDir, id, prev, name)
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, adrDir, name))
		if err != nil {
			t.Fatalf("reading %s/%s: %v", adrDir, name, err)
		}
		first, _, _ := bytes.Cut(body, []byte("\n"))
		if !strings.HasPrefix(string(first), "# "+id+" ") {
			t.Errorf("%s/%s: the first line must be the heading \"# %s <title>\", got %q", adrDir, name, id, first)
		}
		ids[id] = name
	}
	if len(ids) == 0 {
		t.Fatalf("%s holds no decision record", adrDir)
	}
	return ids
}

// trackedFiles lists the files git would commit from this tree: the index plus untracked files
// that are not ignored, so a citation in a new file is judged before its first commit.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v\n%s", root, err, stderr.String())
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("git ls-files listed nothing in %s", root)
	}
	return files
}

// TestADRReferencesResolve is the reference check of audit D4: every ADR id cited anywhere in the
// tree (code, comments, docs, test fixtures) has its record in docs/adr. A citation of a record
// that only exists in the private planning workspace is what a contributor could not follow.
func TestADRReferencesResolve(t *testing.T) {
	root := moduleRoot(t)
	ids := adrIDs(t, root)

	var missing []string
	cited := map[string]bool{}
	for _, rel := range trackedFiles(t, root) {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted in the work tree, still in the index
		}
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, m := range adrCitation.FindAllSubmatchIndex(body, -1) {
			id := "ADR-" + string(body[m[2]:m[3]])
			cited[id] = true
			if _, ok := ids[id]; !ok {
				line := 1 + bytes.Count(body[:m[0]], []byte("\n"))
				missing = append(missing, fmt.Sprintf("%s:%d: %s", rel, line, id))
			}
		}
	}
	if len(missing) > 0 {
		have := make([]string, 0, len(ids))
		for id := range ids {
			have = append(have, id)
		}
		sort.Strings(have)
		t.Fatalf("%d ADR citation(s) with no record in %s (records: %s); vendor the record or cite one that exists:\n%s",
			len(missing), adrDir, strings.Join(have, ", "), strings.Join(missing, "\n"))
	}
	if len(cited) == 0 {
		t.Fatalf("no ADR citation found anywhere: the scan read nothing")
	}
}
