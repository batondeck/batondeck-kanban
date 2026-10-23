package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// The three states decode apart and re-encode as they came.
func TestOptionalRoundTrip(t *testing.T) {
	type probe struct {
		ReviewEnabled Optional[bool]  `json:"reviewEnabled,omitzero"`
		WIPLimit      Nullable[int64] `json:"wipLimit,omitzero"`
	}
	cases := []struct {
		in   string
		want probe
	}{
		{`{}`, probe{}},
		{`{"reviewEnabled":false}`, probe{ReviewEnabled: Some(false)}},
		{`{"wipLimit":null}`, probe{WIPLimit: Null[int64]()}},
		{`{"reviewEnabled":true,"wipLimit":3}`, probe{ReviewEnabled: Some(true), WIPLimit: Value(int64(3))}},
	}
	seen := map[probe]string{}
	for _, c := range cases {
		var got probe
		if err := json.Unmarshal([]byte(c.in), &got); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%s decoded to %+v, want %+v", c.in, got, c.want)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("%s and %s decode to one state %+v", prev, c.in, got)
		}
		seen[got] = c.in
		out, err := json.Marshal(got)
		if err != nil || string(out) != c.in {
			t.Fatalf("%s re-encoded as %s (err %v)", c.in, out, err)
		}
		t.Logf("%s -> %+v -> %s", c.in, got, out)
	}
	// Every migrated field, in its DTO, carrying false, 0 or null: a field left a value drops it on
	// the way back out, and one made a pointer again drops its null.
	for _, c := range []struct {
		in  string
		dst any
	}{
		{`{"projectId":"P-1","reviewEnabled":false}`, &UpdateSettingsReq{}},
		{`{"projectId":"P-1","color":null,"defaultReviewer":null,"quotaEnabled":false,"reviewEnabled":false,"txnCap":null}`, &UpdateSettingsReq{}},
		{`{"boardId":"B-1","columnId":"C-1","projectId":"P-1","order":0,"wipLimit":null}`, &UpdateColumnReq{}},
		{`{"boardId":"B-1","name":"N","projectId":"P-1","status":"READY","order":0}`, &AddColumnReq{}},
		{`{"patch":{"assignee":null,"dueAt":null,"estimate":null,"reviewer":null},"projectId":"P-1","taskId":"T-1","version":1}`, &UpdateTaskReq{}},
		{`{"patch":{"estimate":0},"projectId":"P-1","taskId":"T-1","version":1}`, &UpdateTaskReq{}},
		{`{"leaseId":"L-1","projectId":"P-1","reason":"r","taskId":"T-1","retryable":false}`, &FailTaskReq{}},
		{`{"boardId":"B-1","projectId":"P-1","assignee":null}`, &ListTasksReq{}},
	} {
		if err := json.Unmarshal([]byte(c.in), c.dst); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if out, err := json.Marshal(c.dst); err != nil || string(out) != c.in {
			t.Fatalf("%s re-encoded as %s (err %v)", c.in, out, err)
		}
	}
}

// Optional is conductor's .optional(): null is not one of its values.
func TestOptionalRefusesNull(t *testing.T) {
	var req UpdateSettingsReq
	err := json.Unmarshal([]byte(`{"projectId":"P-1","reviewEnabled":null}`), &req)
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) || te.Field != "reviewEnabled" {
		t.Fatalf("reviewEnabled:null decoded to %+v (err %v), want a type error naming reviewEnabled", req.ReviewEnabled, err)
	}
	t.Logf("reviewEnabled:null -> %v", err)
	var col UpdateColumnReq
	if err := json.Unmarshal([]byte(`{"order":"1"}`), &col); err == nil {
		t.Fatalf(`order:"1" decoded to %+v, want an error`, col.Order)
	}
}

// fail_task's retryable:false reaches the service as false, and absent as unset (conductor's
// default is true, so false is the poison path's only way in).
func TestFailTaskRetryableDecodes(t *testing.T) {
	var sent, absent FailTaskReq
	if err := json.Unmarshal([]byte(`{"leaseId":"L-1","projectId":"P-1","reason":"r","taskId":"T-1","retryable":false}`), &sent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"leaseId":"L-1","projectId":"P-1","reason":"r","taskId":"T-1"}`), &absent); err != nil {
		t.Fatal(err)
	}
	t.Logf("retryable:false -> %+v; absent -> %+v", sent.Retryable, absent.Retryable)
	if sent.Retryable != Some(false) || absent.Retryable.Set {
		t.Fatalf("retryable:false -> %+v, absent -> %+v", sent.Retryable, absent.Retryable)
	}
}

// Over memstore, decoded from the wire: false turns review off, null clears a colour and a WIP
// limit, and a request that omits them changes neither.
func TestTriStateOverMemstore(t *testing.T) {
	f := newFixture(t, Options{})
	settings := func(body string) Settings {
		t.Helper()
		req := UpdateSettingsReq{}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		req.ProjectID = f.project.ID
		if _, err := f.c.Projects().UpdateSettings(f.ctx, f.alice, req); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		got, err := f.c.Projects().Get(f.ctx, f.alice, GetProjectReq{ProjectID: f.project.ID})
		if err != nil {
			t.Fatal(err)
		}
		return got.Project.Settings
	}
	if s := settings(`{"color":"#aabbcc"}`); !s.ReviewEnabled || s.Color != "#aabbcc" {
		t.Fatalf("a new project's review is on and the colour set: %+v", s)
	}
	if s := settings(`{"reviewEnabled":false}`); s.ReviewEnabled || s.Color != "#aabbcc" {
		t.Fatalf("reviewEnabled:false: review %v, colour %q", s.ReviewEnabled, s.Color)
	}
	if s := settings(`{"maxRunsPerTask":3}`); s.ReviewEnabled || s.Color != "#aabbcc" || s.MaxRunsPerTask != 3 {
		t.Fatalf("a patch omitting both: review %v, colour %q", s.ReviewEnabled, s.Color)
	}
	if s := settings(`{"color":null}`); s.ReviewEnabled || s.Color != "" {
		t.Fatalf("color:null: review %v, colour %q", s.ReviewEnabled, s.Color)
	}
	if s := settings(`{"reviewEnabled":true}`); !s.ReviewEnabled {
		t.Fatalf("reviewEnabled:true: review %v", s.ReviewEnabled)
	}

	col := f.board.Columns[0].ID
	limit := func(body string) *float64 {
		t.Helper()
		req := UpdateColumnReq{}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		req.ProjectID, req.BoardID, req.ColumnID = f.project.ID, f.board.ID, col
		got, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, req)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		for _, c := range got.Board.Columns {
			if c.ID == col {
				return c.WIPLimit
			}
		}
		t.Fatalf("column %s gone", col)
		return nil
	}
	if w := limit(`{"wipLimit":4}`); w == nil || *w != 4 {
		t.Fatalf("wipLimit:4 -> %v", w)
	}
	if w := limit(`{"name":"Renamed"}`); w == nil || *w != 4 {
		t.Fatalf("a patch omitting wipLimit -> %v, want 4 unchanged", w)
	}
	if w := limit(`{"wipLimit":null}`); w != nil {
		t.Fatalf("wipLimit:null -> %v, want cleared", *w)
	}
	if w := limit(`{"name":"Again"}`); w != nil {
		t.Fatalf("a patch omitting wipLimit after the clear -> %v", *w)
	}
	_, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col, WIPLimit: Value(int64(0))})
	wantCode(t, err, domain.CodeValidation)

	// order:0 is a position, not an absent order the store appends at the end.
	add := AddColumnReq{}
	if err := json.Unmarshal([]byte(`{"name":"First","status":"READY","order":0}`), &add); err != nil {
		t.Fatal(err)
	}
	add.ProjectID, add.BoardID = f.project.ID, f.board.ID
	made, err := f.c.Boards().AddColumn(f.ctx, f.alice, add)
	if err != nil || made.Column.Order != 0 {
		t.Fatalf("add_column order:0 -> order %v (err %v), want 0", made.Column.Order, err)
	}
}
