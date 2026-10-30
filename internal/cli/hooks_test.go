package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// POST /hooks/{id} is mounted by serve outside every bearer, session and CSRF check, over TCP: a
// signed delivery with no credential but its signature is 202 with the ticket; an unsigned one 401;
// an id of the wrong shape and an unknown one the same flat 404; a body over the outer 512 KiB cap
// 413 before the service sees it (an unknown id too); a body over the integration's own cap 413.
// The delivery expires after the configured window.
func TestServeMountsHooksOutsideEverySession(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bk.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	keys, err := core.OpenKeyring("", func(k string) (string, bool) { return env, k == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	a, err := compose(st, "x", "", log.New(logs, "", 0), keys, service.MediaOptions{}, core.Retention{DeliveriesDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(ctx)
	admin := domain.Caller{Identity: "root", WorkspaceAdmin: true}
	p, err := a.core.Projects().Create(ctx, admin, service.CreateProjectReq{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.core.Boards().Create(ctx, admin, service.CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	in, err := a.core.Oncall().CreateIntegration(ctx, admin, service.CreateIntegrationReq{ProjectID: p.ProjectID, BoardID: b.Board.ID, Name: "n",
		Source: "generic", Auth: service.IntegrationAuthInput{Mode: "hmac"}, Routing: service.IntegrationRouting{Mode: "broadcast"},
		Limits: service.IntegrationLimitsIn{PayloadCapKb: 2}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler("x"))
	defer srv.Close()
	post := func(id string, body []byte, sign bool) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/"+id, bytes.NewReader(body))
		if sign {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			m := hmac.New(sha256.New, []byte(in.Secret))
			m.Write(append([]byte(ts+"."), body...))
			req.Header.Set(service.HeaderTimestamp, ts)
			req.Header.Set(service.HeaderSignature, "v0="+hex.EncodeToString(m.Sum(nil)))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(got), resp.Header
	}
	body := []byte(`{"fingerprint":"f","status":"firing","severity":"critical","title":"Queue backlog"}`)
	code, got, _ := post(in.Integration.ID, body, true)
	var accepted struct {
		Accepted int                  `json:"accepted"`
		Results  []service.HookResult `json:"results"`
	}
	if code != http.StatusAccepted || json.Unmarshal([]byte(got), &accepted) != nil || accepted.Accepted != 1 || accepted.Results[0].Result != "created" {
		t.Fatalf("a signed delivery: %d %s", code, got)
	}
	task, err := st.GetTask(ctx, p.ProjectID, *accepted.Results[0].TaskID)
	if err != nil || task.Title != "Queue backlog" {
		t.Fatalf("the ticket: %+v, %v", task, err)
	}
	// P-109: the delivery's expiry is compose's retention.deliveries_days, not the 30-day default.
	page, err := st.ListDeliveries(ctx, p.ProjectID, in.Integration.ID, store.DeliveryFilter{}, "", 10)
	if err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].ExpiresAt-page.Deliveries[0].TS != (3*24*time.Hour).Milliseconds() {
		t.Fatalf("the delivery %+v, %v: want it to expire 3 days after it was written", page.Deliveries, err)
	}
	if code, got, _ := post(in.Integration.ID, body, false); code != http.StatusUnauthorized || !strings.Contains(got, `"UNAUTHENTICATED"`) {
		t.Fatalf("an unsigned delivery: %d %s", code, got)
	}
	_, unknown, _ := post("I-00000000000000000000000000000000", body, true)
	for _, id := range []string{"I-00000000000000000000000000000000", "bad.id", strings.Repeat("a", 65)} {
		if code, got, _ := post(id, body, true); code != http.StatusNotFound || got != unknown {
			t.Fatalf("id %q: %d %s, want the flat 404 %s", id, code, got, unknown)
		}
	}
	// The outer cap reads at most 512 KiB + 1 before any lookup: an unknown id with a bigger body is
	// 413, not the 404 a read of the whole body and a lookup would answer.
	for _, id := range []string{in.Integration.ID, "I-00000000000000000000000000000000"} {
		if code, got, _ := post(id, bytes.Repeat([]byte("x"), 512<<10+1), true); code != http.StatusRequestEntityTooLarge || !strings.Contains(got, "too_large") {
			t.Fatalf("%s over the outer cap: %d %s", id, code, got)
		}
	}
	// Review B2 of #56: both route-level refusals are a stderr line naming the presented id.
	for _, want := range []string{
		`ingest: refused a delivery to integration "bad.id": not_found: the id is not an integration id's shape`,
		`ingest: refused a delivery to integration "I-00000000000000000000000000000000": too_large: over the route's 524288-byte cap (declared length 524289)`,
		`ingest: refused a delivery to integration "` + in.Integration.ID + `": too_large: over the route's 524288-byte cap (declared length 524289)`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("no refusal line %q", want)
		}
	}
	// A chunked body declares no length: still refused at the cap, and logged.
	chunked, _ := http.NewRequest(http.MethodPost, srv.URL+"/hooks/"+in.Integration.ID, io.MultiReader(bytes.NewReader(bytes.Repeat([]byte("z"), 512<<10+1))))
	resp, err := http.DefaultClient.Do(chunked)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if want := `integration "` + in.Integration.ID + `": too_large: over the route's 524288-byte cap (declared length -1)`; resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(logs.String(), want) {
		t.Errorf("a chunked body over the cap: %d, line %q in the log: %v", resp.StatusCode, want, strings.Contains(logs.String(), want))
	}
	if code, _, _ := post(in.Integration.ID, append(body[:len(body)-1], []byte(`,"description":"`+strings.Repeat("y", 2048)+`"}`)...), true); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the integration's 2 KiB cap: %d", code)
	}
	if !strings.Contains(logs.String(), "ingest: integration "+in.Integration.ID+" refused a delivery's signature") {
		t.Fatalf("no refusal line in %q", logs.String())
	}
}

// hookFaults is the fault a hookFaultStore injects into an ingest's transaction, one at a time:
// mode "" none; "race" (Postgres) commits a competing UpdateTask of task through the base store
// each time the transaction reads it, so its versioned write loses a real race; "stale" (SQLite,
// whose one writer would wait on that commit) answers UpdateTask of task with STALE; "internal"
// fails PutAlertState with a plain error; "validation" and "locked" refuse CreateTask.
type hookFaults struct {
	mu    sync.Mutex
	mode  string
	task  string
	races int
}

func (f *hookFaults) set(mode, task string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.task, f.races = mode, task, 0
}

func (f *hookFaults) get() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode, f.task
}

// hookFaultStore is the real store with hookFaults injected inside its transactions.
type hookFaultStore struct {
	store.Store
	base   store.Store
	faults *hookFaults
	inTx   bool
}

func (s *hookFaultStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return s.Store.Atomically(ctx, func(tx store.Store) error {
		return fn(&hookFaultStore{Store: tx, base: s.base, faults: s.faults, inTx: true})
	})
}

func (s *hookFaultStore) GetTask(ctx context.Context, p, id string) (store.Task, error) {
	t, err := s.Store.GetTask(ctx, p, id)
	if mode, task := s.faults.get(); s.inTx && err == nil && mode == "race" && id == task {
		if _, err := s.base.UpdateTask(ctx, p, id, t.Version, store.TaskPatch{UpdatedBy: "human-racer", UpdatedAt: time.Now().UnixMilli()}); err != nil {
			return store.Task{}, fmt.Errorf("the competing update: %w", err)
		}
		s.faults.mu.Lock()
		s.faults.races++
		s.faults.mu.Unlock()
	}
	return t, err
}

func (s *hookFaultStore) UpdateTask(ctx context.Context, p, id string, v int64, patch store.TaskPatch) (store.Task, error) {
	if mode, task := s.faults.get(); s.inTx && mode == "stale" && id == task {
		return store.Task{}, domain.Stale(v, v+1)
	}
	return s.Store.UpdateTask(ctx, p, id, v, patch)
}

func (s *hookFaultStore) PutAlertState(ctx context.Context, a store.AlertState) error {
	if mode, _ := s.faults.get(); s.inTx && mode == "internal" {
		return errors.New("disk I/O error (injected)")
	}
	return s.Store.PutAlertState(ctx, a)
}

func (s *hookFaultStore) CreateTask(ctx context.Context, in store.CreateTaskInput) (store.Task, error) {
	switch mode, _ := s.faults.get(); {
	case s.inTx && mode == "validation":
		return store.Task{}, domain.Validation("a task needs a title (injected)", nil)
	case s.inTx && mode == "locked":
		return store.Task{}, domain.ConflictLocked("T-9", "someone")
	}
	return s.Store.CreateTask(ctx, in)
}

// hookStack is the composed serve stack over st (wrapped by faults) on a test TCP server, with a
// project and board to file into.
type hookStack struct {
	st      store.Store
	a       *app
	srv     *httptest.Server
	admin   domain.Caller
	project string
	board   string
	logs    *lockedLog
}

func newHookStack(t *testing.T, st store.Store, faults *hookFaults) *hookStack {
	t.Helper()
	ctx := context.Background()
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	keys, err := core.OpenKeyring("", func(k string) (string, bool) { return env, k == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	var wrapped store.Store = st
	if faults != nil {
		wrapped = &hookFaultStore{Store: st, base: st, faults: faults}
	}
	a, err := compose(wrapped, "x", "", log.New(logs, "", 0), keys, service.MediaOptions{}, core.Retention{DeliveriesDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.core.Close(ctx) })
	admin := domain.Caller{Identity: "root", WorkspaceAdmin: true}
	p, err := a.core.Projects().Create(ctx, admin, service.CreateProjectReq{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.core.Boards().Create(ctx, admin, service.CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler("x"))
	t.Cleanup(srv.Close)
	return &hookStack{st: st, a: a, srv: srv, admin: admin, project: p.ProjectID, board: b.Board.ID, logs: logs}
}

type hookAnswer struct {
	status int
	header http.Header
	body   struct {
		Code      string               `json:"code"`
		Message   string               `json:"message"`
		Retryable bool                 `json:"retryable"`
		Results   []service.HookResult `json:"results"`
	}
	raw string
}

func (h *hookStack) post(t *testing.T, id string, body []byte, header map[string]string) hookAnswer {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/hooks/"+id, bytes.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := hookAnswer{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if err := json.Unmarshal(raw, &out.body); err != nil {
		t.Fatalf("the answer %d %q is not JSON: %v", resp.StatusCode, raw, err)
	}
	return out
}

func (h *hookStack) tasks(t *testing.T) []store.Task {
	t.Helper()
	page, err := h.st.ListTasks(context.Background(), h.project, store.TaskFilter{BoardID: h.board}, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return page.Tasks
}

// hookFixture is one real sender's fixture, as the stack test drives it: the integration it is
// delivered to, how a delivery is signed, and the body as a new delivery of the same alert (or,
// fresh, a new alert) under its own delivery id.
type hookFixture struct {
	source string
	auth   service.IntegrationAuthInput
	sign   func(body []byte) map[string]string
	// variant is the fixture under delivery id n; fresh gives its alert a new fingerprint. A grouped
	// fixture lists its resolved alert first, so it lands before the faulted one.
	variant func(t *testing.T, n int, fresh bool) []byte
	before  int // the events that land ahead of the faulted one in a variant
}

func readFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "oncall", "connectors", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func hookFixtures() []hookFixture {
	const pdSecret = "pd-secret-0123"
	const amKey = "am-key-0123456789"
	return []hookFixture{{
		source: "alertmanager",
		auth:   service.IntegrationAuthInput{Mode: "api_key", APIKey: amKey},
		sign:   func([]byte) map[string]string { return map[string]string{service.HeaderAPIKey: amKey} },
		variant: func(t *testing.T, n int, fresh bool) []byte {
			m := readFixture(t, "alertmanager.json")
			if n == 0 {
				b, _ := json.Marshal(m)
				return b
			}
			m["groupKey"] = fmt.Sprintf(`{}:{alertname="HighLatency",n="%d"}`, n)
			alerts := m["alerts"].([]any)
			if fresh {
				alerts[0].(map[string]any)["fingerprint"] = fmt.Sprintf("a1b2c3d4e5f6%04d", n)
			}
			m["alerts"] = []any{alerts[1], alerts[0]}
			b, _ := json.Marshal(m)
			return b
		},
		before: 1,
	}, {
		source: "pagerduty",
		auth:   service.IntegrationAuthInput{Mode: "hmac", ExternalSecret: pdSecret},
		sign: func(body []byte) map[string]string {
			m := hmac.New(sha256.New, []byte(pdSecret))
			m.Write(body)
			return map[string]string{service.HeaderPagerDuty: "v1=" + hex.EncodeToString(m.Sum(nil))}
		},
		variant: func(t *testing.T, n int, fresh bool) []byte {
			m := readFixture(t, "pagerduty.json")
			ev := m["event"].(map[string]any)
			if n > 0 {
				ev["id"] = fmt.Sprintf("01DEN1HNLBC1VITYLBI7MK%04d", n)
			}
			if fresh {
				ev["data"].(map[string]any)["id"] = fmt.Sprintf("PGR%04d", n)
			}
			b, _ := json.Marshal(m)
			return b
		},
	}}
}

// hookEngines are the engines the stack test runs on: SQLite, and Postgres when
// BATONDECK_TEST_POSTGRES_DSN is set; stale is the fault a lost race is injected as.
func hookEngines() []struct {
	name  string
	open  func(t *testing.T) store.Store
	stale string
} {
	return []struct {
		name  string
		open  func(t *testing.T) store.Store
		stale string
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }, "stale"},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }, "race"},
	}
}

// P-233: a verified delivery whose ingest fails retryably is 503 with Retry-After 1, the code
// and the events that landed ahead of it, through the composed serve stack, over the
// Alertmanager and PagerDuty fixtures on both engines: STALE (on Postgres a real lost race, three
// times over, since an unversioned ingest is retried on STALE; on SQLite an injected one) and
// INTERNAL (a plain store error). Alertmanager and PagerDuty retry a 5xx and drop a 4xx other than
// 429, so a 400 here dropped the alert. The sender's retry, once the fault clears, lands the event
// once: the landed ones replayed, one ticket, its occurrences counted once. RATE_LIMITED is 503 too,
// with its own code and Retry-After (P-353: Alertmanager drops a 429 as well); a non-retryable
// refusal (VALIDATION, CONFLICT_LOCKED) stays 400 VALIDATION.
func TestHookAnswersARetryableIngestFailureWith503(t *testing.T) {
	for _, eng := range hookEngines() {
		for _, fx := range hookFixtures() {
			t.Run(eng.name+"/"+fx.source, func(t *testing.T) {
				ctx := context.Background()
				faults := &hookFaults{}
				h := newHookStack(t, eng.open(t), faults)
				in, err := h.a.core.Oncall().CreateIntegration(ctx, h.admin, service.CreateIntegrationReq{ProjectID: h.project, BoardID: h.board, Name: "n",
					Source: service.IntegrationSource(fx.source), Auth: fx.auth, Routing: service.IntegrationRouting{Mode: "broadcast"}})
				if err != nil {
					t.Fatal(err)
				}
				iid := in.Integration.ID
				deliver := func(body []byte) hookAnswer { return h.post(t, iid, body, fx.sign(body)) }
				first := deliver(fx.variant(t, 0, false))
				var incident string
				for _, r := range first.body.Results {
					if r.Result == "created" {
						incident = *r.TaskID
					}
				}
				if first.status != http.StatusAccepted || incident == "" {
					t.Fatalf("the first delivery: %d %s", first.status, first.raw)
				}
				occurrences := func(want int) {
					t.Helper()
					tasks := h.tasks(t)
					if len(tasks) != 1 || tasks[0].ID != incident || !strings.Contains(string(tasks[0].CustomFields), fmt.Sprintf(`"incident.occurrences":%d`, want)) {
						t.Fatalf("want the one incident %s at %d occurrences: %d tasks %+v", incident, want, len(tasks), tasks)
					}
				}
				occurrences(1)

				for n, c := range []struct{ mode, code string }{{eng.stale, "STALE"}, {"internal", "INTERNAL"}} {
					body := fx.variant(t, n+1, false)
					faults.set(c.mode, incident)
					got := deliver(body)
					if got.status != http.StatusServiceUnavailable || got.header.Get("Retry-After") != "1" || got.body.Code != c.code ||
						!got.body.Retryable || len(got.body.Results) != fx.before {
						t.Fatalf("%s: %d Retry-After %q %s, want 503, Retry-After 1, code %s and %d landed result(s)",
							c.mode, got.status, got.header.Get("Retry-After"), got.raw, c.code, fx.before)
					}
					faults.mu.Lock()
					races := faults.races
					faults.mu.Unlock()
					if c.mode == "race" && races != 3 {
						t.Fatalf("the race was lost %d times, want each of the 3 attempts", races)
					}
					if want := "refused: " + c.code; !strings.Contains(h.logs.String(), want) {
						t.Fatalf("no refusal line %q in %s", want, h.logs.String())
					}
					occurrences(n + 1)
					faults.set("", "")
					retry := deliver(body)
					if retry.status != http.StatusAccepted || len(retry.body.Results) != fx.before+1 || retry.header.Get("Retry-After") != "" {
						t.Fatalf("%s: the retry: %d %s", c.mode, retry.status, retry.raw)
					}
					for i, r := range retry.body.Results {
						if landed := i < fx.before; r.Replayed != landed || (!landed && (r.Result != "appended" || *r.TaskID != incident)) {
							t.Fatalf("%s: the retry's result %d: %+v, want the landed ones replayed and the faulted one appended", c.mode, i, r)
						}
					}
					occurrences(n + 2)
					again := deliver(body)
					for _, r := range again.body.Results {
						if again.status != http.StatusAccepted || !r.Replayed {
							t.Fatalf("%s: a second retry: %d %s", c.mode, again.status, again.raw)
						}
					}
					occurrences(n + 2)
				}

				// Not retryable: the sender cannot fix it by retrying, so it stays 400 VALIDATION.
				for n, mode := range []string{"validation", "locked"} {
					faults.set(mode, "")
					got := deliver(fx.variant(t, 10+n, true))
					if got.status != http.StatusBadRequest || got.body.Code != "VALIDATION" || got.header.Get("Retry-After") != "" || got.body.Retryable {
						t.Fatalf("%s: %d %s", mode, got.status, got.raw)
					}
				}
				faults.set("", "")
				occurrences(3)

				// Over the rate budget is 503 RATE_LIMITED with the budget's Retry-After (P-353).
				slow, err := h.a.core.Oncall().CreateIntegration(ctx, h.admin, service.CreateIntegrationReq{ProjectID: h.project, BoardID: h.board, Name: "slow",
					Source: service.IntegrationSource(fx.source), Auth: fx.auth, Routing: service.IntegrationRouting{Mode: "broadcast"}, Limits: service.IntegrationLimitsIn{RatePerMin: 1}})
				if err != nil {
					t.Fatal(err)
				}
				// Its budget is one delivery: a grouped fixture's second event is over it already.
				body := fx.variant(t, 20, true)
				if got := h.post(t, slow.Integration.ID, body, fx.sign(body)); fx.before == 0 && got.status != http.StatusAccepted ||
					fx.before > 0 && (got.status != http.StatusServiceUnavailable || got.body.Code != "RATE_LIMITED" || !got.body.Retryable ||
						len(got.body.Results) != fx.before || got.header.Get("Retry-After") != "60") {
					t.Fatalf("the slow integration's first delivery: %d Retry-After %q %s", got.status, got.header.Get("Retry-After"), got.raw)
				}
				body = fx.variant(t, 21, true)
				if got := h.post(t, slow.Integration.ID, body, fx.sign(body)); got.status != http.StatusServiceUnavailable || got.body.Code != "RATE_LIMITED" ||
					!got.body.Retryable || got.header.Get("Retry-After") != "60" {
					t.Fatalf("over the budget: %d Retry-After %q %s", got.status, got.header.Get("Retry-After"), got.raw)
				}
			})
		}
	}
}

// P-233: every rate-limited answer says when to retry. The integration's budget refills one token
// in ceil(60/ratePerMin) seconds (40 a minute: 2; 1 a minute: 60); the pre-check on the presented id
// refills 120 a second (1). An accepted delivery and an unauthorized one carry no Retry-After.
// P-353: the answer is 503 RATE_LIMITED retryable, not 429, which Alertmanager drops (this test was
// TestHook429CarriesRetryAfter).
func TestHookRateLimitedIs503WithRetryAfter(t *testing.T) {
	for _, eng := range hookEngines() {
		t.Run(eng.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHookStack(t, eng.open(t), nil)
			for _, c := range []struct {
				rate int
				want string
			}{{40, "2"}, {1, "60"}} {
				in, err := h.a.core.Oncall().CreateIntegration(ctx, h.admin, service.CreateIntegrationReq{ProjectID: h.project, BoardID: h.board, Name: fmt.Sprint("r", c.rate),
					Source: "generic", Auth: service.IntegrationAuthInput{Mode: "api_key", APIKey: "key-0123456789"},
					Routing: service.IntegrationRouting{Mode: "broadcast"}, Limits: service.IntegrationLimitsIn{RatePerMin: int64(c.rate)}})
				if err != nil {
					t.Fatal(err)
				}
				key := map[string]string{service.HeaderAPIKey: "key-0123456789"}
				send := func(i int) hookAnswer {
					return h.post(t, in.Integration.ID, fmt.Appendf(nil, `{"fingerprint":"fp-%d","status":"firing","severity":"critical","title":"t"}`, i), key)
				}
				wrong := func() hookAnswer {
					return h.post(t, in.Integration.ID, []byte(`{}`), map[string]string{service.HeaderAPIKey: "wrong-0123456789"})
				}
				// An unauthorized delivery is 401 with no Retry-After, and it spends a token: the
				// charge comes before verification (P-231).
				if got := wrong(); got.status != http.StatusUnauthorized || got.header.Get("Retry-After") != "" {
					t.Fatalf("an unauthorized delivery: %d Retry-After %q", got.status, got.header.Get("Retry-After"))
				}
				for i := range (c.rate+3)/4 - 1 {
					if got := send(i); got.status != http.StatusAccepted || got.header.Get("Retry-After") != "" {
						t.Fatalf("rate %d, delivery %d: %d Retry-After %q", c.rate, i, got.status, got.header.Get("Retry-After"))
					}
				}
				limited := func(got hookAnswer) bool {
					return got.status == http.StatusServiceUnavailable && got.body.Code == "RATE_LIMITED" && got.body.Retryable && got.header.Get("Retry-After") == c.want
				}
				if got := send(99); !limited(got) {
					t.Fatalf("rate %d over the budget: %d Retry-After %q %s, want 503 RATE_LIMITED %s", c.rate, got.status, got.header.Get("Retry-After"), got.raw, c.want)
				}
				if got := wrong(); !limited(got) {
					t.Fatalf("an unauthorized delivery over the budget: %d Retry-After %q %s, want 503 RATE_LIMITED %s", got.status, got.header.Get("Retry-After"), got.raw, c.want)
				}
			}
			// The pre-check: a flood on one presented id (unknown, so 404 until the burst is spent).
			for i := range 500 {
				got := h.post(t, "I-00000000000000000000000000000000", []byte(`{}`), nil)
				if got.status == http.StatusServiceUnavailable {
					if got.header.Get("Retry-After") != "1" || got.body.Code != "RATE_LIMITED" || !got.body.Retryable {
						t.Fatalf("the pre-check's 503: Retry-After %q %s", got.header.Get("Retry-After"), got.raw)
					}
					return
				}
				if got.status != http.StatusNotFound {
					t.Fatalf("flood delivery %d: %d %s", i, got.status, got.raw)
				}
			}
			t.Fatal("500 deliveries to one id never spent the pre-check")
		})
	}
}
