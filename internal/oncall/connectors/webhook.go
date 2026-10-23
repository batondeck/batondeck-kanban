package connectors

import (
	"errors"
	"sort"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// webhook is the custom-webhook connector (conductor webhook.ts): the one source whose shape we do
// not know, told where its fields live by the integration's mapping (applyMapping).
type webhook struct{}

func (webhook) Events(p any, c Context) ([]oncall.Event, error) {
	if c.Mapping == nil || c.Mapping.Extract == nil {
		return nil, errors.New("custom webhook: this integration has no field mapping yet. Set mapping.extract (at minimum extract.fingerprint and extract.title) from the on-call setup screen")
	}
	ms, err := applyMapping(p, *c.Mapping, c)
	if err != nil {
		return nil, err
	}
	out := make([]oncall.Event, len(ms))
	for i, m := range ms {
		out[i] = Cap(oncall.Event{
			Source: "webhook", Fingerprint: m.fingerprint, Status: m.status, Severity: m.severity, Title: m.title,
			Description: m.description, Labels: m.labels, Annotations: m.annotations, Links: m.links, OccurredAt: m.occurredAt,
		})
	}
	return out, nil
}

// DeliveryID is the first event's dedupKey when the mapping names one; a mapping error is left to
// Events, which reports it.
func (webhook) DeliveryID(p any, c Context) string {
	if c.Mapping == nil || c.Mapping.Extract == nil || len(c.Mapping.Extract.DedupKey) == 0 {
		return ""
	}
	ms, err := applyMapping(p, *c.Mapping, c)
	if err != nil || len(ms) == 0 {
		return ""
	}
	return ms[0].dedupKey
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
