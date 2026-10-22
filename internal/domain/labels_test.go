package domain

import (
	"reflect"
	"testing"
)

// The reserved system labels (ADR-0001): a caller cannot add or remove one by hand.

func TestCheckCallerLabelsRefuses(t *testing.T) {
	for _, c := range []struct {
		name               string
		current, requested []string
	}{
		{"poisoned on create", nil, []string{"ingest", "poisoned"}},
		{"cancelled on create", nil, []string{"cancelled"}},
		{"poisoned added by an update", []string{"ingest"}, []string{"ingest", "poisoned"}},
		{"a case and space variant", nil, []string{" Poisoned "}},
		{"poisoned removed by an update", []string{"ingest", "poisoned"}, []string{"ingest"}},
		{"cancelled removed by an update", []string{"cancelled"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckCallerLabels(c.current, c.requested)
			e, ok := AsBatonDeckError(err)
			if !ok || e.Code != CodeValidation {
				t.Fatalf("CheckCallerLabels(%v, %v) = %v, want VALIDATION", c.current, c.requested, err)
			}
		})
	}
}

func TestCheckCallerLabelsAllows(t *testing.T) {
	for _, c := range []struct {
		name               string
		current, requested []string
	}{
		{"ordinary labels on create", nil, []string{"ingest", "deploy"}},
		{"no labels", nil, nil},
		{"an update that keeps the system label", []string{"ingest", "poisoned"}, []string{"poisoned", "deploy"}},
		{"a label that only contains a system name", nil, []string{"not-poisoned", "cancelled-later"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := CheckCallerLabels(c.current, c.requested); err != nil {
				t.Fatalf("CheckCallerLabels(%v, %v) = %v, want nil", c.current, c.requested, err)
			}
		})
	}
}

func TestLabelHelpers(t *testing.T) {
	if got := WithLabel([]string{"a", LabelPoisoned}, LabelPoisoned); !reflect.DeepEqual(got, []string{"a", LabelPoisoned}) {
		t.Fatalf("WithLabel adds no duplicate: %v", got)
	}
	in := []string{"a"}
	if got := WithLabel(in, "b"); !reflect.DeepEqual(got, []string{"a", "b"}) || len(in) != 1 {
		t.Fatalf("WithLabel copies: %v, input %v", got, in)
	}
	if got := WithoutLabel([]string{LabelPoisoned, "a", LabelPoisoned}, LabelPoisoned); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("WithoutLabel: %v", got)
	}
	if !IsSystemLabel("CANCELLED") || IsSystemLabel("ingest") {
		t.Fatal("IsSystemLabel")
	}
}
