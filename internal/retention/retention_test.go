package retention

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

type opener struct {
	name string
	open func(*testing.T) store.Store
}

var backends = []opener{
	{"memstore", func(t *testing.T) store.Store {
		s := memstore.New()
		t.Cleanup(func() { s.Close() })
		if _, err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	}},
	{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
	{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
}

const day = 24 * time.Hour

// AC4: one sweep removes the change-log row older than 7 days, the tool_calls row past 45 days, the
// memory entry past its TTL and the agent session idle past 30 minutes (P-109), and leaves the
// younger ones, on memstore and both engines.
func TestASweepRemovesWhatOutlivedItsWindowAndKeepsTheRest(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			at := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
			appendChange := func(ago time.Duration) int64 {
				id, err := s.AppendChange(ctx, store.ChangeInput{Scope: "project:P-1", Kind: "changed", Ts: at(ago)})
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			oldChange, youngChange := appendChange(8*day), appendChange(6*day)
			call := func(ago time.Duration) store.ToolCall {
				return store.ToolCall{TS: at(ago), Tool: "claim_next", Identity: "alice", Outcome: store.ToolCallOK, Code: "OK"}
			}
			if err := s.InsertToolCalls(ctx, []store.ToolCall{call(46 * day), call(44 * day)}); err != nil {
				t.Fatal(err)
			}
			for _, expires := range []int64{at(time.Minute), at(-time.Hour)} {
				if _, err := s.WriteMemory(ctx, store.WriteMemoryInput{Scope: "agent_global", Key: "k", Value: json.RawMessage(`1`), Author: "alice", ExpiresAt: expires}); err != nil {
					t.Fatal(err)
				}
			}

			for agent, ago := range map[string]time.Duration{"idle": 31 * time.Minute, "busy": 29 * time.Minute} {
				if _, err := s.UpsertAgentSession(ctx, store.AgentSessionStart{IdentityID: "alice", AgentID: agent, AgentName: agent, Kind: "agent", At: at(ago)}); err != nil {
					t.Fatal(err)
				}
			}

			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}),
				Now: func() time.Time { return now }, Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}
			got := sw.Sweep(ctx)
			t.Logf("swept %+v", got)
			if got != (Swept{Memory: 1, ChangeLog: 1, ToolCalls: 1, AgentSessions: 1}) {
				t.Fatalf("swept %+v, want one memory entry, one change-log row, one tool call and one agent session", got)
			}
			if rows, err := s.ListAgentSessions(ctx, "alice", 0); err != nil || len(rows) != 1 || rows[0].AgentID != "busy" {
				t.Fatalf("agent sessions after the sweep: %+v, %v; want only busy", rows, err)
			}
			left, err := s.ReadChanges(ctx, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 1 || left[0].ID != youngChange {
				t.Fatalf("change log after the sweep: %+v, want only %d (not %d)", left, youngChange, oldChange)
			}
			if again := sw.Sweep(ctx); again.ChangeLog != 0 || again.ToolCalls != 0 || again.Memory != 0 || again.AgentSessions != 0 {
				t.Fatalf("a second sweep deleted %+v", again)
			}
			if n, err := s.DeleteToolCallsOlderThan(ctx, 1<<62, 10); err != nil || n != 1 {
				t.Fatalf("tool calls left: %d (%v), want the young one", n, err)
			}
		})
	}
}

// R2.9: a sweep deletes the on-call deliveries past their 30-day expiry (each with its raw body) and
// the raw bodies past their 7-day expiry, and keeps a delivery whose raw body went, on memstore and
// both engines. The expiries are stamped at write time, so the sweep compares with now.
func TestASweepRemovesExpiredDeliveriesAndRawBodies(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			p, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "on-call", CreatedBy: "alice"}, store.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateIntegration(ctx, store.CreateIntegrationInput{ProjectID: p.ID, ID: "I-r", Name: "r", Source: "generic", BoardID: "B-x",
				Auth: json.RawMessage(`{"mode":"api_key"}`), Routing: json.RawMessage(`{"mode":"broadcast"}`), Lifecycle: "correlate",
				Limits: json.RawMessage(`{"ratePerMin":60,"payloadCapKb":256}`), CreatedBy: "alice", CreatedAt: 1})
			if err != nil {
				t.Fatal(err)
			}
			// written 31 days ago (expired), 8 days ago (kept, its raw body expired) and today.
			for _, d := range []struct {
				id  string
				ago time.Duration
			}{{"old", 31 * day}, {"mid", 8 * day}, {"new", 0}} {
				ts := now.Add(-d.ago)
				if _, _, err := s.InsertDelivery(ctx, store.Delivery{ProjectID: p.ID, IntegrationID: "I-r", DeliveryID: d.id, TS: ts.UnixMilli(),
					Kind: store.DeliveryRejected, Result: store.ResultRejected, ExpiresAt: ts.Add(30 * day).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
				if err := s.PutDeliveryRawBody(ctx, p.ID, "I-r", d.id, []byte(d.id), ts.Add(7*day).UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			got := (&Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}).Sweep(ctx)
			if got.Deliveries != 1 || got.RawBodies != 1 {
				t.Fatalf("swept %+v, want one delivery (with its raw body) and one more raw body", got)
			}
			if _, err := s.GetDelivery(ctx, p.ID, "I-r", "old"); err == nil {
				t.Fatal("the 31-day-old delivery outlived its expiry")
			}
			if _, err := s.GetDelivery(ctx, p.ID, "I-r", "mid"); err != nil {
				t.Fatalf("the 8-day-old delivery went with its raw body: %v", err)
			}
			if _, err := s.GetDeliveryRawBody(ctx, p.ID, "I-r", "mid"); err == nil {
				t.Fatal("the 8-day-old raw body outlived its 7 days")
			}
			if body, err := s.GetDeliveryRawBody(ctx, p.ID, "I-r", "new"); err != nil || string(body) != "new" {
				t.Fatalf("today's raw body: %q, %v", body, err)
			}
		})
	}
}

// P-44: a sweep deletes the browser session expired more than LapsedGrace ago and the API token
// expired more than LapsedGrace ago whose label has an older and a newer row, and keeps a session
// and a token lapsed less than LapsedGrace ago, a live session and token, and the oldest and the
// newest row of a lapsed label (label ownership reads them), on memstore and both engines; a second
// sweep deletes nothing.
func TestASweepRemovesLapsedSessionsAndTokens(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			at := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
			if err := s.CreateUser(ctx, store.User{ID: "U-1", Email: "alice", DisplayName: "alice", PasswordHash: "h", CreatedAt: at(60 * day)}); err != nil {
				t.Fatal(err)
			}
			for hash, expires := range map[string]time.Duration{"old": 8 * day, "recent": 6 * day, "live": -day} {
				if err := s.CreateSession(ctx, store.Session{IDHash: hash, UserID: "U-1", CSRFToken: "c", CreatedAt: at(9 * day), LastSeenAt: at(9 * day), ExpiresAt: at(expires)}); err != nil {
					t.Fatal(err)
				}
			}
			for _, tk := range []struct {
				id, label        string
				created, expires time.Duration
			}{
				{"T-first", "scout", 50 * day, 9 * day},  // lapsed past the grace, scout's oldest row: kept
				{"T-old", "scout", 40 * day, 8 * day},    // lapsed past the grace, an older and a newer scout row: deleted
				{"T-recent", "scout", 30 * day, 6 * day}, // lapsed inside the grace: kept
				{"T-live", "scout", 20 * day, -10 * day}, // live: kept
				{"T-last", "probe", 40 * day, 8 * day},   // lapsed past the grace, probe's newest row: kept
			} {
				if err := s.CreateToken(ctx, store.Token{ID: tk.id, IdentityID: "U-1", Subject: "alice", SecretHash: "s-" + tk.id, Label: tk.label,
					Permissions: "[]", CreatedAt: at(tk.created), ExpiresAt: at(tk.expires)}); err != nil {
					t.Fatal(err)
				}
			}
			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}
			if got := sw.Sweep(ctx); got != (Swept{Sessions: 1, Tokens: 1}) {
				t.Fatalf("swept %+v, want one session and one token", got)
			}
			if _, err := s.GetSession(ctx, "old"); err == nil {
				t.Fatal("the session expired 8 days ago outlived the grace")
			}
			for _, h := range []string{"recent", "live"} {
				if _, err := s.GetSession(ctx, h); err != nil {
					t.Fatalf("session %s: %v, want it kept", h, err)
				}
			}
			if _, err := s.GetToken(ctx, "T-old"); err == nil {
				t.Fatal("the token expired 8 days ago with an older and a newer row of its label outlived the grace")
			}
			for _, id := range []string{"T-first", "T-recent", "T-live", "T-last"} {
				if _, err := s.GetToken(ctx, id); err != nil {
					t.Fatalf("token %s: %v, want it kept", id, err)
				}
			}
			if again := sw.Sweep(ctx); again != (Swept{}) {
				t.Fatalf("a second sweep deleted %+v", again)
			}
		})
	}
}

// P-125: a sweep deletes the OAuth code and the access and refresh tokens expired more than
// LapsedGrace ago, used or not, and keeps a code and a token lapsed less than LapsedGrace ago, a live
// code and token, the client and the family, on memstore and both engines; a second sweep deletes
// nothing.
func TestASweepRemovesLapsedOAuthCodesAndTokens(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			at := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
			const res = "https://bk.example/mcp"
			if err := s.CreateOAuthClient(ctx, store.OAuthClient{ClientID: "C-1", ClientName: "c", RedirectURIs: []string{"https://app.example/cb"},
				Scope: "worker", CreatedAt: at(30 * day), LastUsedAt: at(day)}); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateOAuthFamily(ctx, store.OAuthFamily{ID: "F-1", ClientID: "C-1", IdentityID: "U-1", Scope: "worker", Resource: res, CreatedAt: at(30 * day)}); err != nil {
				t.Fatal(err)
			}
			for hash, expires := range map[string]time.Duration{"c-old": 8 * day, "c-recent": 6 * day, "c-live": -5 * time.Minute} {
				if err := s.CreateOAuthCode(ctx, store.OAuthCode{CodeHash: hash, ClientID: "C-1", IdentityID: "U-1", RedirectURI: "https://app.example/cb",
					CodeChallenge: "x", Scope: "worker", Resource: res, CreatedAt: at(expires + 10*time.Minute), ExpiresAt: at(expires)}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ConsumeOAuthCode(ctx, "c-old", at(8*day+time.Minute)); err != nil {
				t.Fatal(err)
			}
			for _, tk := range []struct {
				hash, kind string
				expires    time.Duration
			}{
				{"a-old", store.OAuthAccess, 8 * day},     // past the grace: deleted
				{"r-old", store.OAuthRefresh, 8 * day},    // past the grace, rotated: deleted
				{"r-recent", store.OAuthRefresh, 6 * day}, // inside the grace: kept
				{"a-live", store.OAuthAccess, -time.Hour}, // live: kept
			} {
				if err := s.CreateOAuthToken(ctx, store.OAuthToken{TokenHash: tk.hash, Kind: tk.kind, FamilyID: "F-1", ClientID: "C-1", IdentityID: "U-1",
					Scope: "worker", Resource: res, CreatedAt: at(tk.expires + time.Hour), ExpiresAt: at(tk.expires)}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ConsumeOAuthToken(ctx, "r-old", at(9*day)); err != nil {
				t.Fatal(err)
			}
			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}
			if got := sw.Sweep(ctx); got != (Swept{OAuthCodes: 1, OAuthTokens: 2}) {
				t.Fatalf("swept %+v, want one OAuth code and two OAuth tokens", got)
			}
			if again := sw.Sweep(ctx); again != (Swept{}) {
				t.Fatalf("a second sweep deleted %+v", again)
			}
			for _, h := range []string{"a-old", "r-old"} {
				if _, err := s.GetOAuthToken(ctx, h); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("token %s: %v, want it deleted past the grace", h, err)
				}
			}
			for _, h := range []string{"r-recent", "a-live"} {
				if _, err := s.GetOAuthToken(ctx, h); err != nil {
					t.Fatalf("token %s: %v, want it kept", h, err)
				}
			}
			// A code reads back only through its consume, so the codes are judged after both sweeps.
			if _, err := s.ConsumeOAuthCode(ctx, "c-old", now.UnixMilli()); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("code c-old: %v, want it deleted past the grace", err)
			}
			for _, h := range []string{"c-recent", "c-live"} {
				if _, err := s.ConsumeOAuthCode(ctx, h, now.UnixMilli()); err != nil {
					t.Fatalf("code %s: %v, want it kept", h, err)
				}
			}
			if _, err := s.GetOAuthClient(ctx, "C-1"); err != nil {
				t.Fatalf("the client: %v, want it kept", err)
			}
			if _, err := s.GetOAuthFamily(ctx, "F-1"); err != nil {
				t.Fatalf("the family: %v, want it kept", err)
			}
		})
	}
}

// P-226: a sweep deletes the alert state expired with its ticket DONE, and keeps the expired one of
// an open ticket (its next firing appends through it) and an unexpired one, on memstore and both
// engines; a second sweep deletes nothing.
func TestASweepRemovesTheExpiredAlertStatesOfClosedTickets(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			p, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "on-call", CreatedBy: "alice"}, store.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			bd, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "B", CreatedBy: "alice"}, store.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			task := func(status domain.Status) string {
				for _, c := range bd.Columns {
					if c.Status == status {
						tk, err := s.CreateTask(ctx, store.CreateTaskInput{ProjectID: p.ID, BoardID: bd.ID, ColumnID: c.ID, Status: status, Title: string(status),
							OrderKey: "a0", CreatedBy: "alice", CreatedAt: now.Add(-40 * day).UnixMilli()})
						if err != nil {
							t.Fatal(err)
						}
						return tk.ID
					}
				}
				t.Fatalf("the board has no %s column", status)
				return ""
			}
			open, done := task(domain.Todo), task(domain.Done)
			if _, err := s.CreateIntegration(ctx, store.CreateIntegrationInput{ProjectID: p.ID, ID: "I-r", Name: "r", Source: "generic", BoardID: bd.ID,
				Auth: json.RawMessage(`{"mode":"api_key"}`), Routing: json.RawMessage(`{"mode":"broadcast"}`), Lifecycle: "correlate",
				Limits: json.RawMessage(`{"ratePerMin":60,"payloadCapKb":256}`), CreatedBy: "alice", CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			for _, a := range []struct {
				fp, task string
				expires  time.Duration
			}{
				{"fp-closed", done, -day},     // expired a day ago, its ticket DONE: deleted
				{"fp-open", open, -day},       // expired a day ago, its ticket open: kept
				{"fp-closed-live", done, day}, // its ticket DONE, expiring tomorrow: kept
			} {
				if err := s.PutAlertState(ctx, store.AlertState{ProjectID: p.ID, IntegrationID: "I-r", Fingerprint: a.fp, TaskID: a.task, Occurrences: 1,
					FirstAt: now.Add(-31 * day).UnixMilli(), LastAt: now.Add(-31 * day).UnixMilli(), AlertStatus: "firing", ExpiresAt: now.Add(a.expires).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			}
			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}
			if got := sw.Sweep(ctx); got.AlertStates != 1 {
				t.Fatalf("swept %+v, want one alert state", got)
			}
			if _, err := s.GetAlertState(ctx, p.ID, "I-r", "fp-closed"); err == nil {
				t.Fatal("the expired alert state of a DONE ticket outlived the sweep")
			}
			for _, fp := range []string{"fp-open", "fp-closed-live"} {
				if _, err := s.GetAlertState(ctx, p.ID, "I-r", fp); err != nil {
					t.Fatalf("alert state %s: %v, want it kept", fp, err)
				}
			}
			if again := sw.Sweep(ctx); again.AlertStates != 0 {
				t.Fatalf("a second sweep deleted %+v", again)
			}
		})
	}
}

// fake deletes whatever it is asked to, or fails one step.
type fake struct {
	calls map[string]int
	fail  string
	keeps [][]store.AgentSessionKey // each DeleteIdleAgentSessions call's keep
}

func (f *fake) del(step string, limit int) (int64, error) {
	f.calls[step]++
	if step == f.fail {
		return 3, errors.New("boom")
	}
	return int64(limit), nil
}

func (f *fake) DeleteExpiredMemory(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("memory", limit)
}

func (f *fake) DeleteChangesOlderThan(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("change log", limit)
}

func (f *fake) DeleteToolCallsOlderThan(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("tool calls", limit)
}

func (f *fake) DeleteIdleAgentSessions(_ context.Context, _ int64, limit int, keep []store.AgentSessionKey) (int64, error) {
	f.keeps = append(f.keeps, keep)
	return f.del("agent sessions", limit)
}

func (f *fake) DeleteExpiredSessions(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("sessions", limit)
}

func (f *fake) DeleteLapsedTokens(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("tokens", limit)
}

func (f *fake) DeleteExpiredOAuthCodes(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("oauth codes", limit)
}

func (f *fake) DeleteExpiredOAuthTokens(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("oauth tokens", limit)
}

func (f *fake) DeleteExpiredDeliveries(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("deliveries", limit)
}

func (f *fake) DeleteExpiredDeliveryRawBodies(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("raw bodies", limit)
}

func (f *fake) DeleteExpiredAlertStates(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("alert states", limit)
}

func (f *fake) ListUnreferencedBlobs(context.Context, int64, int) ([]store.Blob, error) {
	return nil, nil
}

// Each table loses at most MaxPerTick rows a sweep, Batch per statement; a step that fails is
// reported and the next one still runs.
func TestASweepIsBoundedAndGoesOnPastAFailure(t *testing.T) {
	f := &fake{calls: map[string]int{}, fail: "change log"}
	var reported []string
	got := (&Sweeper{Store: f, Report: func(step string, _ error) { reported = append(reported, step) }}).Sweep(context.Background())
	if got.Memory != MaxPerTick || got.ToolCalls != MaxPerTick || got.AgentSessions != MaxPerTick || got.Sessions != MaxPerTick || got.Tokens != MaxPerTick || got.OAuthCodes != MaxPerTick || got.OAuthTokens != MaxPerTick || got.Deliveries != MaxPerTick || got.RawBodies != MaxPerTick || got.AlertStates != MaxPerTick || f.calls["memory"] != MaxPerTick/Batch {
		t.Fatalf("swept %+v in %v, want %d per table in %d statements", got, f.calls, MaxPerTick, MaxPerTick/Batch)
	}
	if got.ChangeLog != 3 || f.calls["change log"] != 1 || !slices.Equal(reported, []string{"change log"}) {
		t.Fatalf("the failed step: swept %d in %d calls, reported %v", got.ChangeLog, f.calls["change log"], reported)
	}
}

// P-279: every agent-session statement of a sweep keeps what Live returned, read once per sweep; a
// nil Live keeps none.
func TestASweepKeepsTheLiveAgentSessions(t *testing.T) {
	live := []store.AgentSessionKey{{IdentityID: "alice", AgentID: "claude"}}
	reads := 0
	f := &fake{calls: map[string]int{}}
	(&Sweeper{Store: f, Live: func() []store.AgentSessionKey { reads++; return live }}).Sweep(context.Background())
	if reads != 1 || len(f.keeps) != MaxPerTick/Batch {
		t.Fatalf("Live read %d times for %d statements, want once for %d", reads, len(f.keeps), MaxPerTick/Batch)
	}
	for i, k := range f.keeps {
		if !slices.Equal(k, live) {
			t.Fatalf("statement %d kept %v, want %v", i, k, live)
		}
	}
	f = &fake{calls: map[string]int{}}
	(&Sweeper{Store: f}).Sweep(context.Background())
	if len(f.keeps) == 0 || f.keeps[0] != nil {
		t.Fatalf("a nil Live kept %v, want nil", f.keeps)
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
			for _, agent := range []string{"claude", "gone"} {
				if _, err := s.UpsertAgentSession(ctx, store.AgentSessionStart{IdentityID: "alice", AgentID: agent, AgentName: agent, Kind: "agent", At: now.Add(-31 * time.Minute).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			}
			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }, Live: func() []store.AgentSessionKey { return live }}
			if got := sw.Sweep(ctx); got.AgentSessions != 1 {
				t.Fatalf("swept %+v, want the one agent session not live", got)
			}
			if rows, err := s.ListAgentSessions(ctx, "alice", 0); err != nil || len(rows) != 1 || rows[0].AgentID != "claude" {
				t.Fatalf("agent sessions after the sweep: %+v, %v; want only the live claude", rows, err)
			}
		})
	}
}

// Run sweeps once at once and then every interval, until ctx ends. Each half is waited for, not read
// off a wall-clock window a loaded machine can stall (3 sweeps inside a 70 ms sleep missed when the
// sweeper started 30 ms late): at an hour's interval the one sweep can only be the one at once, and
// at 20 ms the timer brings two more.
func TestRunSweepsAtOnceThenOnTheTimer(t *testing.T) {
	for _, c := range []struct {
		interval time.Duration
		want     int
		what     string
	}{{time.Hour, 1, "the sweep at once"}, {20 * time.Millisecond, 3, "the sweep at once and two on the timer"}} {
		f := &fake{calls: map[string]int{}}
		var began atomic.Int64 // Sweep reads Now once, as it begins
		sw := &Sweeper{Store: f, Now: func() time.Time { began.Add(1); return time.Now() }}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { sw.Run(ctx, c.interval); close(done) }()
		for deadline := time.Now().Add(5 * time.Second); began.Load() < int64(c.want) && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		cancel()
		<-done // Run never returns while a sweep runs: every sweep begun has ended
		n := f.calls["tool calls"] / (MaxPerTick / Batch)
		if n < c.want || (c.interval == time.Hour && n != 1) {
			t.Fatalf("%d sweeps within 5 s at a %v interval, want %d: %s", n, c.interval, c.want, c.what)
		}
	}
}

// The blob step, on memstore and both engines: one sweep hands CollectBlobs the blob unreferenced for
// BlobGrace, and neither a younger unreferenced one nor an old one an attachment holds; with no
// CollectBlobs no blob is listed.
func TestASweepCollectsTheUnreferencedBlobs(t *testing.T) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			s := b.open(t)
			ctx := context.Background()
			hash := func(c string) string { return strings.Repeat(c, 64) }
			for _, c := range []string{"a", "b"} {
				if _, err := s.PutBlob(ctx, hash(c), 1); err != nil {
					t.Fatal(err)
				}
			}
			p, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: "alice"}, store.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			bd, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "B", CreatedBy: "alice"}, store.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			task, err := s.CreateTask(ctx, store.CreateTaskInput{ProjectID: p.ID, BoardID: bd.ID, ColumnID: bd.Columns[0].ID, Status: bd.Columns[0].Status, Title: "t", OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
			if err != nil {
				t.Fatal(err)
			}
			att, err := s.CreateAttachment(ctx, store.CreateAttachmentInput{ProjectID: p.ID, TaskID: task.ID, MimeType: "text/plain", Bytes: 1, Kind: store.AttachmentFile, CreatedBy: "alice"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetAttachmentReady(ctx, p.ID, task.ID, att.ID, hash("b")); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Add(BlobGrace + time.Minute)
			var got [][]string
			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}), Now: func() time.Time { return now },
				Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }, CollectBlobs: func(_ context.Context, h []string) { got = append(got, h) }}
			if swept := sw.Sweep(ctx); swept.Blobs != 1 || len(got) != 1 || !slices.Equal(got[0], []string{hash("a")}) {
				t.Fatalf("swept %+v, collected %v, want the one unreferenced blob", swept, got)
			}
			got = nil
			sw.Now = time.Now
			if swept := sw.Sweep(ctx); swept.Blobs != 0 || got != nil {
				t.Fatalf("within the grace: swept %+v, collected %v", swept, got)
			}
			sw.Now, sw.CollectBlobs = func() time.Time { return now }, nil
			if swept := sw.Sweep(ctx); swept.Blobs != 0 {
				t.Fatalf("with no CollectBlobs: swept %+v", swept)
			}
		})
	}
}
