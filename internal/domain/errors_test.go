package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// retryableByCode pins the codes and their retryability, which are public API: a client's retry
// loop reads them. AllCodes must list exactly these, so a 14th code fails until it is classified here.
var retryableByCode = map[Code]bool{
	"VALIDATION":         false,
	"UNAUTHENTICATED":    false,
	"FORBIDDEN":          false,
	"NOT_FOUND":          false,
	"STALE":              true,
	"CONFLICT_LOCKED":    false,
	"LEASE_EXPIRED":      false,
	"INVALID_TRANSITION": false,
	"WIP_EXCEEDED":       false,
	"CYCLE_DETECTED":     false,
	"QUOTA_EXCEEDED":     false,
	"RATE_LIMITED":       true,
	"INTERNAL":           true,
}

func TestErrorCodes(t *testing.T) {
	codes := AllCodes()
	if len(codes) != 13 || len(retryableByCode) != 13 {
		t.Fatalf("AllCodes has %d codes, the pin %d; both must be 13", len(codes), len(retryableByCode))
	}
	for _, c := range codes {
		if _, ok := retryableByCode[c]; !ok {
			t.Fatalf("code %s is not pinned", c)
		}
	}
	for code, want := range retryableByCode {
		t.Run(fmt.Sprintf("%s is retryable=%v", code, want), func(t *testing.T) {
			if got := NewError(code, "x", nil).Retryable; got != want {
				t.Fatalf("retryable = %v", got)
			}
		})
	}
}

func TestErrorModel(t *testing.T) {
	t.Run("marks STALE / RATE_LIMITED / INTERNAL retryable, others not", func(t *testing.T) {
		for _, c := range []struct {
			err  *BatonDeckError
			want bool
		}{
			{Stale(1, 2), true},
			{NewError(CodeRateLimited, "x", nil), true},
			{NewError(CodeInternal, "x", nil), true},
			{NewError(CodeForbidden, "x", nil), false},
			{NotFound("Task", "T-1"), false},
			{InvalidTransition("A", "B", nil, nil), false},
		} {
			if c.err.Retryable != c.want {
				t.Fatalf("%s retryable = %v", c.err.Code, c.err.Retryable)
			}
		}
	})
	t.Run("toStructured carries code/message/retryable + optional details", func(t *testing.T) {
		b, err := json.Marshal(Stale(3, 5))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"code": "STALE", "message": "Version mismatch: expected 3, found 5", "retryable": true,
			"details": map[string]any{"expected": float64(3), "actual": float64(5)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("json = %s", b)
		}
		b, _ = json.Marshal(NewError(CodeInternal, "x", nil))
		if string(b) != `{"code":"INTERNAL","message":"x","retryable":true}` {
			t.Fatalf("json without details = %s", b)
		}
	})
	t.Run("isBatonDeckError type guard", func(t *testing.T) {
		if e, ok := AsBatonDeckError(fmt.Errorf("wrapped: %w", NewError(CodeForbidden, "x", nil))); !ok || e.Code != CodeForbidden {
			t.Fatalf("wrapped BatonDeckError not found: %v %v", e, ok)
		}
		if _, ok := AsBatonDeckError(fmt.Errorf("plain")); ok {
			t.Fatal("a plain error reads as a BatonDeckError")
		}
	})
}

func TestErrorConstructors(t *testing.T) {
	if e := Stale(3, 5); e.Code != CodeStale || !reflect.DeepEqual(e.Details, map[string]any{"expected": int64(3), "actual": int64(5)}) {
		t.Fatalf("Stale = %+v", e)
	}
	e := ConflictLocked("T-7", "agent-a")
	if e.Code != CodeConflictLocked || e.Message != "Task T-7 is claimed by agent-a" ||
		!reflect.DeepEqual(e.Details, map[string]any{"taskId": "T-7", "owner": "agent-a"}) {
		t.Fatalf("ConflictLocked = %+v", e)
	}
}
