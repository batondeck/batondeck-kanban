package retention

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
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

// AC4: one sweep removes the change-log row older than 7 days, the tool_calls row past 45 days and
// the memory entry past its TTL, and leaves the younger ones, on memstore and both engines.
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

			sw := &Sweeper{Store: s, Windows: WindowsOf(core.Retention{ChangeLogDays: 7, ToolCallsDays: 45}),
				Now: func() time.Time { return now }, Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}
			got := sw.Sweep(ctx)
			t.Logf("swept %+v", got)
			if got != (Swept{Memory: 1, ChangeLog: 1, ToolCalls: 1}) {
				t.Fatalf("swept %+v, want one memory entry, one change-log row and one tool call", got)
			}
			left, err := s.ReadChanges(ctx, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 1 || left[0].ID != youngChange {
				t.Fatalf("change log after the sweep: %+v, want only %d (not %d)", left, youngChange, oldChange)
			}
			if again := sw.Sweep(ctx); again.ChangeLog != 0 || again.ToolCalls != 0 || again.Memory != 0 {
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

// fake deletes whatever it is asked to, or fails one step.
type fake struct {
	calls map[string]int
	fail  string
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

func (f *fake) DeleteExpiredDeliveries(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("deliveries", limit)
}

func (f *fake) DeleteExpiredDeliveryRawBodies(_ context.Context, _ int64, limit int) (int64, error) {
	return f.del("raw bodies", limit)
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
	if got.Memory != MaxPerTick || got.ToolCalls != MaxPerTick || got.Deliveries != MaxPerTick || got.RawBodies != MaxPerTick || f.calls["memory"] != MaxPerTick/Batch {
		t.Fatalf("swept %+v in %v, want %d per table in %d statements", got, f.calls, MaxPerTick, MaxPerTick/Batch)
	}
	if got.ChangeLog != 3 || f.calls["change log"] != 1 || !slices.Equal(reported, []string{"change log"}) {
		t.Fatalf("the failed step: swept %d in %d calls, reported %v", got.ChangeLog, f.calls["change log"], reported)
	}
}

// Run sweeps once at once and then every interval, until ctx ends.
func TestRunSweepsAtOnceThenOnTheTimer(t *testing.T) {
	f := &fake{calls: map[string]int{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Sweeper{Store: f}).Run(ctx, 20*time.Millisecond); close(done) }()
	time.Sleep(70 * time.Millisecond)
	cancel()
	<-done
	if n := f.calls["tool calls"] / (MaxPerTick / Batch); n < 3 {
		t.Fatalf("%d sweeps in 70 ms at a 20 ms interval, want at least 3", n)
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
