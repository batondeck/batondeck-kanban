package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// make bench's measurements (R3.5; plan §Performance "Claim path", "Idle cost"), on the serve
// composition: the claim race through real go-sdk clients (claimRace, R1.18's), and the idle cost
// of the device session directory and /api/stream, counted by the engine's own statement count
// (store.StatementCounter), which sees the bus tail, the HTTP handlers and the directory's sweep
// whatever their context. They live here for the reason proofs_test.go does: only the composition
// root may hold the MCP SDK and reach the store (ADR-0003 §C rows 6 and 7). bench/ judges what they
// write. Run without -race: the race detector multiplies the claim latency about 15× (P-195).

// benchDirEnv names the directory the measurements are written to; unset, TestBench skips, so make
// test and make check do not run it.
const benchDirEnv = "BATONDECK_BENCH_DIR"

const (
	benchClaimers = 50  // plan §Performance: 50 agents × claim_next on one board
	benchRounds   = 5   // the race's latency is the median round's, after one warm-up round
	benchTargetMs = 25  // the plan's p95 target on local SQLite: printed, not enforced
	benchEntries  = 200 // ADR-0004: 200 device-directory entries
	benchStreams  = 10  // and 10 /api/stream clients
	benchHold     = 60 * time.Second
	benchPerEntry = 3 // ADR-0004: at most 3 statements per entry per sweep
)

// metric is one number make bench records: bench/ compares it with the baseline by gate ("p20"
// fails beyond +20 %, "exact" fails on any rise, "" is not compared).
type metric struct {
	Engine string  `json:"engine"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Gate   string  `json:"gate"`
}

func writeMetrics(t *testing.T, path string, ms []metric) {
	t.Helper()
	b, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// TestBench measures, per SQL engine servicetest selects, the claim race and then the idle cost,
// and writes them to $BATONDECK_BENCH_DIR/cli-<engine>.json. The races run one engine at a time;
// the idle holds, which are CPU-idle by construction, run side by side so make bench stays inside
// three minutes on both engines.
func TestBench(t *testing.T) {
	dir := os.Getenv(benchDirEnv)
	if dir == "" {
		t.Skipf("%s is unset: make bench sets it", benchDirEnv)
	}
	var engines []string
	for _, e := range servicetest.Engines(t) {
		if e != servicetest.Memstore {
			engines = append(engines, e)
		}
	}
	got := map[string][]metric{}
	var mu sync.Mutex
	add := func(ms ...metric) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range ms {
			got[m.Engine] = append(got[m.Engine], m)
		}
	}
	t.Run("race", func(t *testing.T) {
		for _, e := range engines {
			t.Run(e, func(t *testing.T) { add(benchRace(t, e)...) })
		}
	})
	// No registry entry is written: the entries have no socket, so the directory never reads it.
	old := wakeRegistry
	reg := shortDir(t, "bkbr")
	wakeRegistry = func() string { return reg }
	t.Cleanup(func() { wakeRegistry = old })
	t.Run("idle", func(t *testing.T) {
		for _, e := range engines {
			t.Run(e, func(t *testing.T) {
				t.Parallel()
				add(benchIdle(t, e)...)
			})
		}
	})
	// Written even after a failure: bench/ then names every number that regressed, and refuses a
	// baseline from the run.
	for e, m := range got {
		writeMetrics(t, filepath.Join(dir, "cli-"+e+".json"), m)
	}
}

// benchRace is a warm-up round and then benchRounds claim races of benchClaimers workers, each
// round over a fresh serve composition, and the median round's p50 and p95. The warm-up's numbers
// are logged and dropped: the first round on a process (and on Postgres, a fresh database) is the
// one that varied most between runs.
func benchRace(t *testing.T, engine string) []metric {
	var rounds []race
	for i := range benchRounds + 1 {
		t.Run(fmt.Sprintf("round%d", i), func(t *testing.T) {
			r := join(t, engine).claimRace(benchClaimers)
			if r.winners != benchClaimers || r.failed != 0 {
				t.Fatalf("%d distinct winners, %d errors; want %d and 0", r.winners, r.failed, benchClaimers)
			}
			var deciles []string
			for q := 1; q <= 9; q++ {
				deciles = append(deciles, fmt.Sprint(r.p(float64(q)/10).Round(100*time.Microsecond)))
			}
			t.Logf("%s round %d%s: %d claim_next at once: %d distinct winners, 0 errors; p50 %v p95 %v max %v; wall %v (%.0f claims/s); deciles %s",
				engine, i, map[bool]string{true: " (warm-up, dropped)"}[i == 0], benchClaimers, r.winners, r.p(0.5).Round(100*time.Microsecond), r.p(0.95).Round(100*time.Microsecond),
				r.p(1).Round(100*time.Microsecond), r.wall.Round(100*time.Microsecond), float64(benchClaimers)/r.wall.Seconds(), strings.Join(deciles, " "))
			if i > 0 {
				rounds = append(rounds, r)
			}
		})
	}
	if len(rounds) != benchRounds {
		t.Fatalf("%d of %d rounds ran", len(rounds), benchRounds)
	}
	median := func(f func(race) time.Duration) time.Duration {
		v := make([]time.Duration, 0, len(rounds))
		for _, r := range rounds {
			v = append(v, f(r))
		}
		slices.Sort(v)
		return v[len(v)/2]
	}
	p50 := median(func(r race) time.Duration { return r.p(0.5) })
	p95 := median(func(r race) time.Duration { return r.p(0.95) })
	slowest := median(func(r race) time.Duration { return r.p(1) })
	wall := median(func(r race) time.Duration { return r.wall })
	verdict := ""
	if engine == servicetest.SQLite {
		verdict = fmt.Sprintf(" (the plan's target on local SQLite is < %d ms: met)", benchTargetMs)
		if ms(p95) >= benchTargetMs {
			verdict = fmt.Sprintf(" (the plan's target on local SQLite is < %d ms: MISSED)", benchTargetMs)
		}
	}
	t.Logf("%s: claim race, median of %d rounds: p50 %.1f ms, p95 %.1f ms%s, max %.1f ms, wall %.1f ms",
		engine, benchRounds, ms(p50), ms(p95), verdict, ms(slowest), ms(wall))
	return []metric{
		{engine, "claim race winners (of 50)", float64(benchClaimers), "", ""},
		{engine, "claim race p50", ms(p50), "ms", "p20"},
		{engine, "claim race p95", ms(p95), "ms", "p20"},
		{engine, "claim race max", ms(slowest), "ms", ""},
		{engine, "claim race wall", ms(wall), "ms", ""},
	}
}

// streamClient is one /api/stream subscriber and the events it has read.
type streamClient struct {
	ready  chan struct{}
	events sync.Map // name → *atomic.Int64
}

func (c *streamClient) count(name string) int64 {
	if v, ok := c.events.Load(name); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

// benchIdle holds benchEntries directory entries and benchStreams /api/stream clients for benchHold
// with no write, over a fresh serve composition, and counts the statements the engine issues: those
// of the directory's sweep (it fires wakeSweep after serve starts, inside the hold) and every other
// one, which must be none.
func benchIdle(t *testing.T, engine string) []metric {
	st := servicetest.OpenStore(t, engine)
	sc, ok := st.(store.StatementCounter)
	if !ok {
		t.Fatalf("%T does not count its statements", st)
	}
	logs := &lockedLog{}
	logger := log.New(logs, "", 0)
	sock := filepath.Join(shortDir(t, "bkb"), "bk.sock")
	lns, err := listenAll([]string{"tcp://127.0.0.1:0", "unix://" + sock}, logger)
	if err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", "", logger, testKeys(t), service.MediaOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- serveUntil(ctx, lns, a, "x", logger) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	})
	base := "http://" + lns[0].Addr().String()
	if code, _ := httpDo(t, &http.Client{Timeout: 5 * time.Second}, http.MethodGet, base+"/healthz", "", ""); code != http.StatusOK {
		t.Fatalf("healthz %d", code)
	}

	// The project, its board and benchStreams members, each with a password and an API token.
	accounts := &auth.Accounts{Store: st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}
	mk := func(login string) person {
		u, err := accounts.Add(ctx, login, login, joinPassword, false)
		if err != nil {
			t.Fatal(err)
		}
		tok, info, err := (&auth.TokenService{Store: st}).Create(ctx, u.ID, "", "bench", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return person{id: u.ID, token: tok, tokenID: info.ID}
	}
	owner := mk("ada")
	p, err := a.core.Projects().Create(ctx, owner.caller(), service.CreateProjectReq{Name: "bench"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.core.Boards().Create(ctx, owner.caller(), service.CreateBoardReq{ProjectID: p.ProjectID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	viewers := make([]string, benchStreams)
	people := make([]person, benchStreams)
	for i := range viewers {
		viewers[i] = fmt.Sprintf("v%02d", i)
		people[i] = mk(viewers[i])
		if _, err := a.core.Projects().AddMember(ctx, owner.caller(), service.AddMemberReq{ProjectID: p.ProjectID, IdentityID: people[i].id, Role: domain.RoleWorker}); err != nil {
			t.Fatal(err)
		}
	}

	// benchEntries live processes of this uid, registered over the socket-only route, a member's
	// token each in turn. No socket: the sweep rechecks an entry with no adapter as it does any.
	sockClient := unixHTTP(sock)
	for i := range benchEntries {
		cmd := exec.Command("sleep", "3600")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		body := fmt.Sprintf(`{"pid":%d,"role":"worker"}`, cmd.Process.Pid)
		if code, got := httpDo(t, sockClient, http.MethodPost, "http://x/internal/wake-session", people[i%benchStreams].token, body); code != http.StatusCreated {
			t.Fatalf("register entry %d: %d %s", i, code, got)
		}
	}
	if n := len(a.dir.Entries()); n != benchEntries {
		t.Fatalf("the directory holds %d entries, want %d", n, benchEntries)
	}

	// benchStreams /api/stream subscribers of the board, one per member, signed in by password.
	j := &joined{t: t, engine: engine, st: st, a: a, url: base}
	clients := make([]*streamClient, benchStreams)
	for i, login := range viewers {
		c := &streamClient{ready: make(chan struct{})}
		clients[i] = c
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/stream?projectId="+p.ProjectID+"&boardId="+b.Board.ID, nil)
		for _, ck := range j.login(login) {
			req.AddCookie(ck)
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("stream %s: %d %s", login, resp.StatusCode, body)
		}
		t.Cleanup(func() { resp.Body.Close() })
		go func() {
			var once sync.Once
			scan := bufio.NewScanner(resp.Body)
			for scan.Scan() {
				name, ok := strings.CutPrefix(scan.Text(), "event: ")
				if !ok {
					continue
				}
				v, _ := c.events.LoadOrStore(name, new(atomic.Int64))
				v.(*atomic.Int64).Add(1)
				if name == "ready" {
					once.Do(func() { close(c.ready) })
				}
			}
		}()
	}
	for i, c := range clients {
		select {
		case <-c.ready:
		case <-time.After(5 * time.Second):
			t.Fatalf("stream %d sent no ready event", i)
		}
	}

	// quiet waits until the count has held still for d, or for at most limit, and answers it.
	quiet := func(d, limit time.Duration) int64 {
		last, since, give := sc.StatementsIssued(), time.Now(), time.Now().Add(limit)
		for {
			time.Sleep(2 * time.Millisecond)
			if n := sc.StatementsIssued(); n != last {
				last, since = n, time.Now()
			} else if time.Since(since) >= d {
				return n
			}
			if time.Now().After(give) {
				t.Errorf("the count never held still for %v in %v: something issues statements with no write", d, limit)
				return last
			}
		}
	}
	c0 := quiet(time.Second, 5*time.Second)
	t0 := time.Now()
	t.Logf("%s: %d entries and %d streams up %.1f s after serve started; holding %v with no write",
		engine, benchEntries, benchStreams, t0.Sub(started).Seconds(), benchHold)
	// The sweep's ticker starts after started, so nothing before slot is the sweep.
	slot := started.Add(wakeSweep - 50*time.Millisecond)
	var pre, before int64
	last := c0
	for {
		time.Sleep(2 * time.Millisecond)
		n := sc.StatementsIssued()
		if n == last {
			if time.Now().After(started.Add(wakeSweep + 15*time.Second)) {
				t.Fatalf("no sweep in %v: the count held at %d", wakeSweep+15*time.Second, n)
			}
			continue
		}
		if time.Now().Before(slot) {
			pre += n - last
			last = n
			continue
		}
		before = last
		break
	}
	swept := time.Now()
	after := quiet(300*time.Millisecond, 10*time.Second)
	sweep := after - before
	sweepTook := time.Since(swept) - 300*time.Millisecond
	if wait := time.Until(t0.Add(benchHold)); wait > 0 {
		time.Sleep(wait)
	}
	end := sc.StatementsIssued()
	held := time.Since(t0)
	post := end - after
	between := pre + post
	perEntry := float64(sweep) / benchEntries
	var pings, presence int64
	for _, c := range clients {
		pings += c.count("ping")
		presence += c.count("presence")
	}
	t.Logf("%s: idle hold %.1f s: the sweep at %.1f s issued %d statements in about %v (%.2f per entry, bound %d); between sweeps %d; the %d streams read %d pings and %d presence frames",
		engine, held.Seconds(), swept.Sub(started).Seconds(), sweep, sweepTook.Round(time.Millisecond), perEntry, benchPerEntry, between, benchStreams, pings, presence)
	if n := len(a.dir.Entries()); n != benchEntries {
		t.Errorf("the directory holds %d entries after the hold, want %d:\n%s", n, benchEntries, logs.String())
	}
	switch {
	case sweep == 0:
		t.Errorf("the sweep issued 0 statements: the count saw nothing, so it proves nothing")
	case perEntry > benchPerEntry:
		t.Errorf("the sweep issued %d statements for %d entries: %.2f per entry, over the bound of %d", sweep, benchEntries, perEntry, benchPerEntry)
	}
	if pre > 0 {
		t.Errorf("%d statement(s) in the hold before the sweep was due", pre)
	}
	if post > 0 {
		t.Errorf("%d statement(s) after the sweep, before the hold ended", post)
	}
	if pings < benchStreams {
		t.Errorf("the streams read %d pings in %.0f s, want at least %d: a stream was not live", pings, held.Seconds(), benchStreams)
	}
	return []metric{
		{engine, "idle: directory entries", benchEntries, "", ""},
		{engine, "idle: /api/stream clients", benchStreams, "", ""},
		{engine, "idle: hold", float64(held.Round(100 * time.Millisecond).Seconds()), "s", ""},
		{engine, "idle: statements per entry per sweep", perEntry, "", "exact"},
		{engine, "idle: statements between sweeps", float64(between), "", "exact"},
	}
}
