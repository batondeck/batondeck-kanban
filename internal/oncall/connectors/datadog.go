package connectors

import (
	"errors"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// datadog is a Datadog monitor webhook in the documented $-variable template (conductor
// datadog.ts): {alert_id, aggreg_key, event_type, transition, title, body, severity, link, tags}.
// $ALERT_ID is shared by every group of a multi-alert monitor; $AGGREG_KEY names the group, so the
// fingerprint is alert_id:aggreg_key, alert_id alone when the template carries no aggreg_key.
type datadog struct{}

// tagLabels reads $TAGS, a comma-joined string or an array: k:v is labels[k] = v.
func tagLabels(tags any) map[string]string {
	var parts []string
	switch x := tags.(type) {
	case string:
		parts = strings.Split(x, ",")
	case []any:
		for _, t := range x {
			if s, ok := t.(string); ok {
				parts = append(parts, s)
			}
		}
	}
	labels := map[string]string{}
	for _, raw := range parts {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if i := strings.Index(tag, ":"); i > 0 {
			labels[tag[:i]] = tag[i+1:]
		} else {
			labels[tag] = ""
		}
	}
	return labels
}

func (datadog) Events(p any, c Context) ([]oncall.Event, error) {
	m, ok := record(p)
	if !ok {
		return nil, errors.New("unrecognizable datadog payload: expected a JSON object")
	}
	fingerprint := idString(m["alert_id"])
	if fingerprint == "" {
		return nil, errors.New("unrecognizable datadog payload: missing alert_id")
	}
	// One group's recovery must not resolve another group's ticket.
	if group := idString(m["aggreg_key"]); group != "" {
		fingerprint += ":" + group
	}
	transition := str(m["transition"])
	if transition == "" {
		transition = str(m["alert_transition"])
	}
	transition = lower(transition)
	status := oncall.StatusFiring
	if transition == "recovered" {
		status = oncall.StatusResolved
	}
	annotations := map[string]string{}
	if transition != "" {
		annotations["datadog_transition"] = transition
	}
	if et := str(m["event_type"]); et != "" {
		annotations["datadog_event_type"] = et
	}
	links := []string{}
	if l := str(m["link"]); l != "" {
		links = append(links, l)
	}
	severity, title := str(m["severity"]), str(m["title"])
	if severity == "" {
		severity = "warning"
	}
	if title == "" {
		title = "Datadog alert"
	}
	date := m["date"]
	if date == nil {
		date = m["occurredAt"]
	}
	return []oncall.Event{Cap(oncall.Event{
		Source: "datadog", Fingerprint: fingerprint, Status: status, Severity: severity, Title: title,
		Description: str(m["body"]), Labels: tagLabels(m["tags"]), Annotations: annotations, Links: links, OccurredAt: timeOrNow(date, c.Now),
	})}, nil
}

// DeliveryID is none: the template defines no per-delivery id.
func (datadog) DeliveryID(any, Context) string { return "" }
