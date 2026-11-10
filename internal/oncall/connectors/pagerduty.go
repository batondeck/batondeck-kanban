package connectors

import (
	"errors"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// pagerduty is a PagerDuty v3 webhook subscription (conductor pagerduty.ts): {event: {id,
// event_type, occurred_at, data: {id, type, title, status, urgency, html_url, assignees, priority,
// service}}}. The first assignee's PagerDuty user id is the event's upstream assignee, the
// integration's pdUserMap input. A subscription may carry every event type, and only an incident.*
// event whose data is the incident is an alert: any other (a note, a status update, a responder,
// a service event, the subscription's pagey.ping test) is no event, accepted and filed as nothing.
type pagerduty struct{}

func (pagerduty) Events(p any, c Context) ([]oncall.Event, error) {
	m, _ := record(p)
	event, ok := record(m["event"])
	if !ok {
		return nil, errors.New("unrecognizable pagerduty payload: expected a v3 { event } envelope")
	}
	data, ok := record(event["data"])
	if !ok {
		return nil, errors.New("unrecognizable pagerduty payload: event.data missing")
	}
	eventType := str(event["event_type"])
	if eventType == "" {
		eventType = "incident.triggered"
	}
	// data.type absent is an older payload whose data is the incident.
	if !strings.HasPrefix(eventType, "incident.") || data["type"] != nil && str(data["type"]) != "incident" {
		return nil, nil
	}
	fingerprint := str(data["id"])
	if fingerprint == "" {
		return nil, errors.New("unrecognizable pagerduty payload: event.data.id missing")
	}
	urgency := str(data["urgency"])
	priority := ""
	if pr, ok := record(data["priority"]); ok {
		priority = str(pr["summary"])
	}
	severity := "warning"
	switch {
	case urgency == "high":
		severity = "critical"
	case urgency == "low":
		severity = "warning"
	case priority != "":
		severity = lower(priority)
	}
	labels := map[string]string{}
	if urgency != "" {
		labels["urgency"] = urgency
	}
	if svc, ok := record(data["service"]); ok && str(svc["summary"]) != "" {
		labels["pagerduty_service"] = str(svc["summary"])
	}
	if priority != "" {
		labels["pagerduty_priority"] = priority
	}
	annotations := map[string]string{"pagerduty_event_type": eventType}
	if s := str(data["status"]); s != "" {
		annotations["pagerduty_status"] = s
	}
	assignee := ""
	if as, ok := data["assignees"].([]any); ok && len(as) > 0 {
		if a, ok := record(as[0]); ok {
			assignee = str(a["id"])
		}
	}
	links := []string{}
	if u := str(data["html_url"]); u != "" {
		links = append(links, u)
	}
	// incident.resolved is resolved; triggered, reopened, acknowledged, escalated … are firing.
	status := oncall.StatusFiring
	if eventType == "incident.resolved" {
		status = oncall.StatusResolved
	}
	title := str(data["title"])
	if title == "" {
		title = "PagerDuty incident"
	}
	return []oncall.Event{Cap(oncall.Event{
		Source: "pagerduty", Fingerprint: fingerprint, Status: status, Severity: severity, Title: title, Labels: labels,
		Annotations: annotations, Links: links, UpstreamAssignee: assignee, OccurredAt: timeOrNow(event["occurred_at"], c.Now),
	})}, nil
}

// DeliveryID is the v3 envelope's event.id.
func (pagerduty) DeliveryID(p any, _ Context) string {
	m, _ := record(p)
	e, _ := record(m["event"])
	return str(e["id"])
}
