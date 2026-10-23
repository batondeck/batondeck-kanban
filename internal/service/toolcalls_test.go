package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

func (f *fixture) toolCallRows(t *testing.T) int64 {
	t.Helper()
	n, err := f.st.DeleteToolCallsOlderThan(f.ctx, 1<<62, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

var okCall = RecordToolCallReq{Tool: "claim_next", Outcome: "ok", Code: "OK", LatencyMs: 12}

// Positive control: recorded calls land on a flush, with the caller's identity and agent.
func TestToolCallsRecordAndFlush(t *testing.T) {
	f := newFixture(t, Options{})
	for range 3 {
		if _, err := f.c.ToolCalls().Record(f.ctx, f.alice, okCall); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.c.FlushToolCalls(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.toolCallRows(t); n != 3 {
		t.Fatalf("%d rows landed, want 3", n)
	}
}

// AC5: with the store closed, Record returns without error and the flush logs the failure.
func TestToolCallsRecordNeverFailsTheCaller(t *testing.T) {
	f := newFixture(t, Options{})
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	if err != nil {
		t.Fatalf("Record failed its caller: %v", err)
	}
	flushErr := f.c.FlushToolCalls(f.ctx)
	logged := f.logs.String()
	t.Logf("Record returned %v; flush returned %v; logged %q", err, flushErr, strings.TrimSpace(logged))
	if flushErr == nil || !strings.Contains(logged, "tool_calls: dropped a batch of 1 rows") || !strings.Contains(logged, "closed") {
		t.Fatalf("the store failure was not logged: %q", logged)
	}
}

// A row the store would refuse is dropped and logged on Record, so it cannot refuse the batch the
// good rows ride in.
func TestToolCallsABadRowDoesNotSinkTheBatch(t *testing.T) {
	f := newFixture(t, Options{})
	for _, req := range []RecordToolCallReq{okCall, {Tool: "", Outcome: "ok", Code: "OK"}, {Tool: "t", Outcome: "maybe", Code: "OK"}, {Tool: "t", Outcome: "ok", Code: "OK", LatencyMs: -1}, okCall} {
		if _, err := f.c.ToolCalls().Record(f.ctx, f.alice, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.c.ToolCalls().Record(f.ctx, domain.Caller{}, okCall); err != nil {
		t.Fatal(err)
	}
	if err := f.c.FlushToolCalls(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.toolCallRows(t); n != 2 || strings.Count(f.logs.String(), "dropped a row") != 4 {
		t.Fatalf("%d rows landed (want 2); log %q", n, f.logs.String())
	}
}

// A full buffer drops the newest rows and the next flush logs the count.
func TestToolCallsFullBufferDropsAndCounts(t *testing.T) {
	f := newFixture(t, Options{ToolCallBuffer: 2, ToolCallBatch: 10})
	for range 5 {
		_, _ = f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	}
	if err := f.c.FlushToolCalls(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.toolCallRows(t); n != 2 || !strings.Contains(f.logs.String(), "dropped 3 rows") {
		t.Fatalf("%d rows landed (want 2); log %q", n, f.logs.String())
	}
}

// The background flusher writes a full batch without being asked, and Close flushes the rest.
func TestToolCallsBackgroundFlushAndClose(t *testing.T) {
	f := newFixture(t, Options{ToolCallBatch: 2, ToolCallFlush: time.Hour})
	_, _ = f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	_, _ = f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	deadline := time.Now().Add(2 * time.Second)
	var landed int64
	for landed < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		landed += f.toolCallRows(t)
	}
	if landed != 2 {
		t.Fatalf("a full batch was not flushed: %d rows", landed)
	}
	_, _ = f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	if err := f.c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.toolCallRows(t); n != 1 {
		t.Fatalf("Close left %d rows unflushed", 1-n)
	}
}
