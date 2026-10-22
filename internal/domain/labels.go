package domain

import (
	"fmt"
	"slices"
	"strings"
)

// The system labels (ADR-0001). The server writes them: cancelled by cancel_task, poisoned by the
// poison outcome of ApplyFailure, removed again by Unpoison. A caller never adds or removes one.
const (
	LabelCancelled = "cancelled"
	LabelPoisoned  = "poisoned"
)

// SystemLabels returns the reserved labels.
func SystemLabels() []string {
	return []string{LabelCancelled, LabelPoisoned}
}

// IsSystemLabel reports whether l names a system label, ignoring case and surrounding space, so
// "Poisoned " cannot pass for a hand-made one.
func IsSystemLabel(l string) bool {
	n := strings.ToLower(strings.TrimSpace(l))
	for _, s := range SystemLabels() {
		if n == s {
			return true
		}
	}
	return false
}

// CheckCallerLabels refuses a caller's label write that adds or removes a system label: current is
// the task's labels (nil on create), requested the labels the caller asks for. Keeping a system label
// the task already carries is allowed, so a full-list update that leaves it in place passes, provided
// it is spelled exactly as the constant: the server's WithLabel and WithoutLabel match exactly.
func CheckCallerLabels(current, requested []string) error {
	for _, s := range SystemLabels() {
		had, wants := hasSystemLabel(current, s), hasSystemLabel(requested, s)
		switch {
		case wants && !had:
			return Validation(fmt.Sprintf("label %q is a system label and cannot be added by hand", s),
				map[string]any{"label": s, "systemLabels": SystemLabels()})
		case had && !wants:
			return Validation(fmt.Sprintf("label %q is a system label and cannot be removed by hand", s),
				map[string]any{"label": s, "systemLabels": SystemLabels()})
		case wants && !slices.Contains(requested, s):
			// Kept, but respelled: stored as given, it would escape WithoutLabel's exact match.
			return Validation(fmt.Sprintf("label %q is a system label and must be kept as %q", s, s),
				map[string]any{"label": s, "systemLabels": SystemLabels()})
		}
	}
	return nil
}

// WithLabel returns a copy of labels with l appended unless it is already there.
func WithLabel(labels []string, l string) []string {
	out := append([]string(nil), labels...)
	for _, x := range out {
		if x == l {
			return out
		}
	}
	return append(out, l)
}

// WithoutLabel returns a copy of labels with every l removed.
func WithoutLabel(labels []string, l string) []string {
	out := make([]string, 0, len(labels))
	for _, x := range labels {
		if x != l {
			out = append(out, x)
		}
	}
	return out
}

func hasSystemLabel(labels []string, s string) bool {
	for _, l := range labels {
		if strings.ToLower(strings.TrimSpace(l)) == s {
			return true
		}
	}
	return false
}
