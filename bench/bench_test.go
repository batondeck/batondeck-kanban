// Package bench_test is make bench's judge (R3.5; plan §Performance "make bench … fails on regression
// beyond 20 %"). The measurements run elsewhere and write their numbers to $BATONDECK_BENCH_DIR:
// internal/cli's TestBench (the claim race through real MCP clients, the idle cost of the device
// session directory and /api/stream) and test/perf's TestQueriesPerCall (the per-call statement
// pins). TestBench here gathers them into results.json with the machine and the Go that measured
// them, compares them with the committed baseline.json, and holds README.md's performance section
// to baseline.json. The other tests prove the comparison itself.
package bench_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	dirEnv      = "BATONDECK_BENCH_DIR"
	baselineEnv = "BATONDECK_BENCH_BASELINE" // "write": make bench-baseline
	dsnEnv      = "BATONDECK_TEST_POSTGRES_DSN"
	engineEnv   = "BATONDECK_TEST_ENGINE"

	resultsFile  = "results.json"
	baselineFile = "baseline.json"
	readmeFile   = "../README.md"
	readmeBegin  = "<!-- bench:begin: make bench-baseline writes this block from bench/baseline.json; make bench checks it -->"
	readmeEnd    = "<!-- bench:end -->"

	// latencyGate is the rise a "p20" number may take before make bench fails.
	latencyGate = 0.20
)

// metric is one recorded number. Gate "p20" fails beyond +20 % (the latencies), "exact" on any rise
// (the statement counts, which are deterministic: +20 % of a count of 6 would let a seventh
// statement through), "" is recorded and not compared.
type metric struct {
	Engine string  `json:"engine"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Gate   string  `json:"gate"`
}

// machine names the hardware a run measured on. Name is what like-for-like means: the model, the
// CPU and its core count. The host name is not recorded, since the README prints all of this.
type machine struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	CPU       string `json:"cpu"`
	Cores     int    `json:"cores"`
	MemoryGiB int    `json:"memoryGiB"`
	OS        string `json:"os"`
}

type results struct {
	Machine machine  `json:"machine"`
	Go      string   `json:"go"`
	Metrics []metric `json:"metrics"`
}

// TestBench gathers the measurements, writes results.json, and then either rewrites baseline.json
// and the README block (make bench-baseline) or compares with them (make bench).
func TestBench(t *testing.T) {
	dir := os.Getenv(dirEnv)
	if dir == "" {
		t.Skipf("%s is unset: make bench sets it", dirEnv)
	}
	ms, err := gather(dir, engines())
	if err != nil {
		t.Fatal(err)
	}
	cur := results{Machine: thisMachine(), Go: runtime.Version(), Metrics: ms}
	if err := writeJSON(resultsFile, cur); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote bench/%s:\n%s", resultsFile, render(cur))
	if os.Getenv(baselineEnv) == "write" {
		if err := writeJSON(baselineFile, cur); err != nil {
			t.Fatal(err)
		}
		if err := writeReadme(readmeFile, cur); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote bench/%s and README.md's performance section from this run: the PR that commits them shows the reviewer the new baseline", baselineFile)
		return
	}
	var base results
	if err := readJSON(baselineFile, &base); err != nil {
		t.Fatalf("%v: make bench-baseline records one", err)
	}
	fails, notes := compare(base, cur)
	for _, n := range notes {
		t.Log(n)
	}
	for _, f := range fails {
		t.Error(f)
	}
	if len(fails) == 0 {
		t.Logf("no regression against bench/%s (%s, %s)", baselineFile, base.Machine.Name, base.Go)
	}
	if err := checkReadme(readmeFile, base); err != nil {
		t.Error(err)
	}
}

// engines is the engines the measurements ran on: BATONDECK_TEST_ENGINE's one, else SQLite, and
// Postgres too when BATONDECK_TEST_POSTGRES_DSN is set.
func engines() []string {
	if e := os.Getenv(engineEnv); e != "" {
		return []string{e}
	}
	out := []string{"sqlite"}
	if os.Getenv(dsnEnv) != "" {
		out = append(out, "postgres")
	}
	return out
}

// gather reads cli-<engine>.json and pins-<engine>.json for every engine from dir. A missing file
// fails: a go test run whose -run matches nothing prints [no tests to run] and passes, so a file
// is the only proof a measurement ran.
func gather(dir string, engines []string) ([]metric, error) {
	var out []metric
	for _, e := range engines {
		for _, part := range []string{"cli", "pins"} {
			name := filepath.Join(dir, part+"-"+e+".json")
			var ms []metric
			if err := readJSON(name, &ms); err != nil {
				return nil, fmt.Errorf("%w: the %s measurement on %s did not run or did not pass", err, part, e)
			}
			if len(ms) == 0 {
				return nil, fmt.Errorf("%s holds no number", name)
			}
			for _, m := range ms {
				if m.Engine != e {
					return nil, fmt.Errorf("%s holds a %s number (%s)", name, m.Engine, m.Name)
				}
			}
			out = append(out, ms...)
		}
	}
	return out, nil
}

// compare holds cur to base, like for like: the same machine name and engine, number by number.
// fails is every regression, named with both values and the rise; notes says what was not compared
// and why.
func compare(base, cur results) (fails, notes []string) {
	if base.Machine.Name != cur.Machine.Name {
		return nil, []string{fmt.Sprintf("NOT COMPARED: the baseline was measured on %q, this run on %q; the absolute checks still ran. make bench-baseline records this machine's (and the PR shows it)",
			base.Machine.Name, cur.Machine.Name)}
	}
	if base.Go != cur.Go {
		notes = append(notes, fmt.Sprintf("the baseline was measured with %s, this run with %s", base.Go, cur.Go))
	}
	type key struct{ engine, name string }
	was := map[key]metric{}
	baseEngines := map[string]bool{}
	for _, m := range base.Metrics {
		was[key{m.Engine, m.Name}] = m
		baseEngines[m.Engine] = true
	}
	unmatched := map[string]bool{}
	for _, m := range cur.Metrics {
		if m.Gate == "" {
			continue
		}
		if !baseEngines[m.Engine] {
			unmatched[m.Engine] = true
			continue
		}
		b, ok := was[key{m.Engine, m.Name}]
		if !ok {
			notes = append(notes, fmt.Sprintf("%s %q: no baseline number, not compared", m.Engine, m.Name))
			continue
		}
		if f := judge(b, m); f != "" {
			fails = append(fails, f)
		}
	}
	for _, e := range slices.Sorted(maps.Keys(unmatched)) {
		notes = append(notes, fmt.Sprintf("NOT COMPARED: no %s baseline on %q; make bench-baseline records one", e, cur.Machine.Name))
	}
	return fails, notes
}

// judge is "" when cur holds against base under cur's gate, else the regression in words.
func judge(base, cur metric) string {
	rise := "from 0"
	if base.Value != 0 {
		rise = fmt.Sprintf("%+.1f %%", 100*(cur.Value-base.Value)/base.Value)
	}
	switch cur.Gate {
	case "p20":
		if cur.Value > base.Value*(1+latencyGate) {
			return fmt.Sprintf("REGRESSION %s %s: %s → %s (%s), beyond the +%.0f %% the gate allows",
				cur.Engine, cur.Name, show(base), show(cur), rise, 100*latencyGate)
		}
	case "exact":
		if cur.Value > base.Value {
			return fmt.Sprintf("REGRESSION %s %s: %s → %s (%s); a statement count may not rise: remove the statement, or raise the baseline with make bench-baseline and the reason in the PR",
				cur.Engine, cur.Name, show(base), show(cur), rise)
		}
	}
	return ""
}

func show(m metric) string {
	v := strconv.FormatFloat(m.Value, 'f', -1, 64)
	if m.Unit == "ms" || m.Unit == "s" {
		v = strconv.FormatFloat(m.Value, 'f', 1, 64)
	} else if m.Value != math.Trunc(m.Value) {
		v = strconv.FormatFloat(m.Value, 'f', 2, 64)
	}
	if m.Unit != "" {
		v += " " + m.Unit
	}
	return v
}

// render is r as the README's table: one row per number, one column per engine.
func render(r results) string {
	var engines, names []string
	vals := map[[2]string]metric{}
	gate := map[string]string{}
	for _, m := range r.Metrics {
		if !slices.Contains(engines, m.Engine) {
			engines = append(engines, m.Engine)
		}
		if !slices.Contains(names, m.Name) {
			names = append(names, m.Name)
		}
		vals[[2]string{m.Engine, m.Name}] = m
		gate[m.Name] = m.Gate
	}
	var b strings.Builder
	m := r.Machine
	fmt.Fprintf(&b, "Measured by `make bench` on %s: %s, %d cores, %d GiB, %s; %s.\n\n", m.Model, m.CPU, m.Cores, m.MemoryGiB, m.OS, r.Go)
	b.WriteString("| Number |")
	for _, e := range engines {
		fmt.Fprintf(&b, " %s |", e)
	}
	b.WriteString(" Gate |\n|---|")
	for range engines {
		b.WriteString("---:|")
	}
	b.WriteString("---|\n")
	for _, n := range names {
		fmt.Fprintf(&b, "| %s |", n)
		for _, e := range engines {
			if v, ok := vals[[2]string{e, n}]; ok {
				fmt.Fprintf(&b, " %s |", show(v))
			} else {
				b.WriteString(" — |")
			}
		}
		switch gate[n] {
		case "p20":
			b.WriteString(" fails beyond +20 % |\n")
		case "exact":
			b.WriteString(" fails on any rise |\n")
		default:
			b.WriteString(" recorded |\n")
		}
	}
	if p95, ok := vals[[2]string{"sqlite", "claim race p95"}]; ok {
		verdict := "met"
		if p95.Value >= 25 {
			verdict = "**missed**"
		}
		fmt.Fprintf(&b, "\nThe plan's target, claim race p95 < 25 ms on local SQLite: %s (%s).\n", verdict, show(p95))
	}
	return b.String()
}

// writeReadme replaces the README's block between readmeBegin and readmeEnd with render(r).
func writeReadme(path string, r results) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	i, j := bytes.Index(src, []byte(readmeBegin)), bytes.Index(src, []byte(readmeEnd))
	if i < 0 || j < i {
		return fmt.Errorf("%s has no %q … %q block", path, readmeBegin, readmeEnd)
	}
	out := slices.Concat(src[:i+len(readmeBegin)], []byte("\n"+render(r)), src[j:])
	return os.WriteFile(path, out, 0o644)
}

// checkReadme fails when the README's block is not render(base).
func checkReadme(path string, base results) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	i, j := bytes.Index(src, []byte(readmeBegin)), bytes.Index(src, []byte(readmeEnd))
	if i < 0 || j < i {
		return fmt.Errorf("%s has no %q … %q block", path, readmeBegin, readmeEnd)
	}
	if got, want := string(src[i+len(readmeBegin):j]), "\n"+render(base); got != want {
		return fmt.Errorf("README.md's performance block is not bench/%s's numbers: make bench-baseline writes it; it reads\n%s\nand bench/%s renders\n%s", baselineFile, got, baselineFile, want)
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// thisMachine reads the hardware and OS from the host (sysctl and sw_vers on macOS, /proc and
// /etc/os-release on Linux); what cannot be read stays empty.
func thisMachine() machine {
	m := machine{Cores: runtime.NumCPU()}
	out := func(name string, args ...string) string {
		b, err := exec.Command(name, args...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	switch runtime.GOOS {
	case "darwin":
		m.Model = out("sysctl", "-n", "hw.model")
		m.CPU = out("sysctl", "-n", "machdep.cpu.brand_string")
		if n, err := strconv.ParseInt(out("sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
			m.MemoryGiB = int(n >> 30)
		}
		m.OS = strings.TrimSpace("macOS " + out("sw_vers", "-productVersion") + " (" + out("sw_vers", "-buildVersion") + ")")
	case "linux":
		if b, err := os.ReadFile("/sys/devices/virtual/dmi/id/product_name"); err == nil {
			m.Model = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "model name" {
					m.CPU = strings.TrimSpace(v)
					break
				}
			}
		}
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			var kb int64
			if _, err := fmt.Sscanf(string(b), "MemTotal: %d kB", &kb); err == nil {
				m.MemoryGiB = int((kb + 1<<19) >> 20)
			}
		}
		if b, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
					m.OS = strings.Trim(v, `"`)
				}
			}
		}
	}
	if m.OS == "" {
		m.OS = runtime.GOOS
	}
	m.OS += " " + runtime.GOARCH
	m.Name = fmt.Sprintf("%s, %s, %d cores", cmp.Or(m.Model, "unknown model"), cmp.Or(m.CPU, "unknown CPU"), m.Cores)
	return m
}

// The comparison's own proofs: each gate fails on the regression it names and holds on the
// largest change it allows (the positive control), and a different machine is not compared.

func run(engine, name string, v float64, unit, gate string) metric {
	return metric{Engine: engine, Name: name, Value: v, Unit: unit, Gate: gate}
}

func on(name string, ms ...metric) results {
	return results{Machine: machine{Name: name}, Go: "go1.x", Metrics: ms}
}

func TestLatencyGate(t *testing.T) {
	base := on("m", run("sqlite", "claim race p95", 40, "ms", "p20"))
	if f, _ := compare(base, on("m", run("sqlite", "claim race p95", 48, "ms", "p20"))); len(f) != 0 {
		t.Fatalf("+20.0 %% failed (the gate is beyond 20 %%): %v", f)
	}
	f, _ := compare(base, on("m", run("sqlite", "claim race p95", 48.6, "ms", "p20")))
	if len(f) != 1 || !strings.Contains(f[0], "40.0 ms → 48.6 ms (+21.5 %)") {
		t.Fatalf("+21.5 %% gave %q, want one regression naming both values and the rise", f)
	}
	t.Log(f[0])
}

func TestCountGate(t *testing.T) {
	base := on("m", run("postgres", "claim_next: snapshot cached", 5, "", "exact"))
	if f, _ := compare(base, on("m", run("postgres", "claim_next: snapshot cached", 5, "", "exact"))); len(f) != 0 {
		t.Fatalf("an unchanged count failed: %v", f)
	}
	f, _ := compare(base, on("m", run("postgres", "claim_next: snapshot cached", 6, "", "exact")))
	if len(f) != 1 || !strings.Contains(f[0], "5 → 6 (+20.0 %)") {
		t.Fatalf("5 → 6 gave %q, want one regression: +20.0 %% is within the latency gate, and a count may not rise at all", f)
	}
	t.Log(f[0])
	zero := on("m", run("sqlite", "idle: statements between sweeps", 0, "", "exact"))
	f, _ = compare(zero, on("m", run("sqlite", "idle: statements between sweeps", 1, "", "exact")))
	if len(f) != 1 || !strings.Contains(f[0], "0 → 1 (from 0)") {
		t.Fatalf("0 → 1 gave %q, want one regression", f)
	}
	t.Log(f[0])
}

func TestUngatedAndUnmatched(t *testing.T) {
	base := on("m", run("sqlite", "claim race max", 30, "ms", ""))
	f, notes := compare(base, on("m", run("sqlite", "claim race max", 300, "ms", ""), run("postgres", "claim race p95", 99, "ms", "p20")))
	if len(f) != 0 {
		t.Fatalf("a recorded-only number or an engine with no baseline failed: %v", f)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "no postgres baseline") {
		t.Fatalf("notes %q, want the missing postgres baseline named", notes)
	}
}

func TestOtherMachineIsNotCompared(t *testing.T) {
	base := on("Mac14,5, Apple M2 Max, 12 cores", run("sqlite", "claim race p95", 1, "ms", "p20"))
	f, notes := compare(base, on("x86, Xeon, 4 cores", run("sqlite", "claim race p95", 100, "ms", "p20")))
	if len(f) != 0 || len(notes) != 1 || !strings.HasPrefix(notes[0], "NOT COMPARED") {
		t.Fatalf("another machine: fails %v notes %v, want none and one NOT COMPARED", f, notes)
	}
}

func TestGatherRefusesAMissingMeasurement(t *testing.T) {
	dir := t.TempDir()
	if err := writeJSON(filepath.Join(dir, "cli-sqlite.json"), []metric{run("sqlite", "claim race p95", 1, "ms", "p20")}); err != nil {
		t.Fatal(err)
	}
	if _, err := gather(dir, []string{"sqlite"}); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gather with no pins file: %v, want a missing-file error", err)
	}
	if err := writeJSON(filepath.Join(dir, "pins-sqlite.json"), []metric{run("sqlite", "get_task: snapshot cached", 2, "", "exact")}); err != nil {
		t.Fatal(err)
	}
	if ms, err := gather(dir, []string{"sqlite"}); err != nil || len(ms) != 2 {
		t.Fatalf("gather with both files: %d numbers, %v", len(ms), err)
	}
}

func TestReadmeBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	r := on("m", run("sqlite", "claim race p95", 33.6, "ms", "p20"))
	if err := os.WriteFile(path, []byte("# x\n\n"+readmeBegin+"\nstale\n"+readmeEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkReadme(path, r); err == nil {
		t.Fatal("a stale block passed the check")
	}
	if err := writeReadme(path, r); err != nil {
		t.Fatal(err)
	}
	if err := checkReadme(path, r); err != nil {
		t.Fatalf("the written block failed the check: %v", err)
	}
}
