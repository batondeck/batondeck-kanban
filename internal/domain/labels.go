package domain

import (
	"fmt"
	"strings"
)

// The system labels (ADR-0001). The server writes them: cancelled by cancel_task and removed again
// when the task leaves DONE (Reopened), poisoned by the poison outcome of ApplyFailure and removed
// again by Unpoison. A caller never adds or removes one.
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
// the task already carries is allowed, so a full-list update that leaves it in place passes. Every
// requested entry naming a system label must be spelled exactly as its constant, because the
// server's WithLabel and WithoutLabel match exactly: a respelling beside the constant would outlive
// an un-poison.
func CheckCallerLabels(current, requested []string) error {
	for _, l := range requested {
		if canonical := strings.ToLower(strings.TrimSpace(l)); IsSystemLabel(l) && l != canonical {
			return Validation(fmt.Sprintf("label %q is a system label and must be spelled %q", l, canonical),
				map[string]any{"label": l, "systemLabels": SystemLabels()})
		}
	}
	for _, s := range SystemLabels() {
		had, wants := hasSystemLabel(current, s), hasSystemLabel(requested, s)
		switch {
		case wants && !had:
			return Validation(fmt.Sprintf("label %q is a system label and cannot be added by hand", s),
				map[string]any{"label": s, "systemLabels": SystemLabels()})
		case had && !wants:
			return Validation(fmt.Sprintf("label %q is a system label and cannot be removed by hand", s),
				map[string]any{"label": s, "systemLabels": SystemLabels()})
		}
	}
	return nil
}

// Reopened returns labels as a task leaving DONE carries them: without cancelled, which marks a DONE
// task that was abandoned rather than delivered, so a reopened task is counted as done when it is
// delivered again.
func Reopened(labels []string) []string {
	return WithoutLabel(labels, LabelCancelled)
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
