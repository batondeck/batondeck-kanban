package connectors

import (
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// promFamilyEvents is the Prometheus webhook family's mapping (conductor prom-family.ts): Grafana
// unified alerting and Alertmanager's webhook_config share one envelope, a top-level alerts[] whose
// entries are {status, labels, annotations, startsAt, endsAt, generatorURL, fingerprint}. One event
// per entry.
func promFamilyEvents(payload any, source, fallbackTitle string, linkKeys []string, c Context) ([]oncall.Event, error) {
	m, ok := record(payload)
	alerts, isArray := m["alerts"].([]any)
	if !ok || !isArray {
		return nil, fmt.Errorf("unrecognizable %s payload: expected an object with an alerts[] array", source)
	}
	out := make([]oncall.Event, 0, len(alerts))
	for _, entry := range alerts {
		alert, _ := record(entry)
		labels, annotations := stringMap(alert["labels"]), stringMap(alert["annotations"])
		resolved := alert["status"] == "resolved"
		// The correlation key; absent, a stable one from the label set (what Prometheus hashes).
		fingerprint := str(alert["fingerprint"])
		if fingerprint == "" {
			fingerprint = Sha256Hex(sortedJSON(labels))
		}
		links := []string{}
		for _, k := range linkKeys {
			if l := str(alert[k]); l != "" {
				links = append(links, l)
			}
		}
		occurred := timeOrNow(alert["startsAt"], c.Now)
		if resolved {
			if t, ok := realTime(alert["endsAt"]); ok {
				occurred = t
			}
		}
		status := oncall.StatusFiring
		if resolved {
			status = oncall.StatusResolved
		}
		// An empty severity="" is a legal relabel artefact: it falls back like an absent one.
		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}
		title := annotations["summary"]
		if title == "" {
			title = labels["alertname"]
		}
		if title == "" {
			title = fallbackTitle
		}
		out = append(out, Cap(oncall.Event{
			Source: source, Fingerprint: fingerprint, Status: status, Severity: severity, Title: title,
			Description: annotations["description"], Labels: labels, Annotations: annotations, Links: links, OccurredAt: occurred,
		}))
	}
	return out, nil
}

// alertmanager is Prometheus Alertmanager's native webhook_config (conductor alertmanager.ts).
type alertmanager struct{}

func (alertmanager) Events(p any, c Context) ([]oncall.Event, error) {
	return promFamilyEvents(p, "alertmanager", "Alertmanager alert", []string{"generatorURL"}, c)
}

// DeliveryID is groupKey.
func (alertmanager) DeliveryID(p any, _ Context) string {
	m, _ := record(p)
	return str(m["groupKey"])
}

// grafana is Grafana's unified-alerting webhook contact point (conductor grafana.ts).
type grafana struct{}

func (grafana) Events(p any, c Context) ([]oncall.Event, error) {
	return promFamilyEvents(p, "grafana", "Grafana alert", []string{"generatorURL", "silenceURL", "dashboardURL", "panelURL"}, c)
}

// DeliveryID is none: Grafana's groupKey repeats across a group's successive notifications.
func (grafana) DeliveryID(any, Context) string { return "" }
