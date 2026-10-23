package connectors

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// gcp is Google Cloud Monitoring's webhook, schema 1.2 (conductor gcp.ts): {version, incident:
// {…}}. The open and the close carry the same incident_id, so it is the fingerprint and a close
// lands on the ticket its open filed; the delivery id is <incident_id>:<state>, so the two are
// distinct deliveries. Times are Unix epoch seconds.
type gcp struct{}

func epochToISO(v any) (string, bool) {
	var n float64
	switch x := v.(type) {
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return "", false
		}
		n = f
	case interface{ Float64() (float64, error) }:
		f, err := x.Float64()
		if err != nil {
			return "", false
		}
		n = f
	default:
		return "", false
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return "", false
	}
	return isoTime(time.UnixMilli(int64(n * 1000))), true
}

// gcpAbsentSeverity are Cloud Monitoring's words for "no severity set" ("No severity" verbatim).
var gcpAbsentSeverity = map[string]bool{"": true, "no severity": true, "none": true, "unspecified": true}

func incidentOf(p any) (map[string]any, error) {
	m, _ := record(p)
	inc, ok := record(m["incident"])
	if !ok {
		return nil, errors.New("unrecognizable gcp payload: expected an object with an `incident` object")
	}
	return inc, nil
}

func (gcp) Events(p any, c Context) ([]oncall.Event, error) {
	inc, err := incidentOf(p)
	if err != nil {
		return nil, err
	}
	incidentID := str(inc["incident_id"])
	if incidentID == "" {
		return nil, errors.New("gcp payload: incident.incident_id is required")
	}
	// Documented as open or closed; anything else may mean the opposite of firing.
	state := str(inc["state"])
	if state != "open" && state != "closed" {
		return nil, fmt.Errorf("gcp payload: incident.state must be 'open' or 'closed', got %v", inc["state"])
	}
	status := oncall.StatusFiring
	if state == "closed" {
		status = oncall.StatusResolved
	}
	resource, _ := record(inc["resource"])
	metric, _ := record(inc["metric"])
	doc, _ := record(inc["documentation"])
	labels := map[string]string{}
	put := func(dst map[string]string, k string, v any) {
		if s := str(v); s != "" {
			dst[k] = s
		}
	}
	put(labels, "project", inc["scoping_project_id"])
	put(labels, "policy", inc["policy_name"])
	put(labels, "condition", inc["condition_name"])
	put(labels, "resource_type", resource["type"])
	name := inc["resource_display_name"]
	if name == nil {
		name = inc["resource_name"]
	}
	put(labels, "resource_name", name)
	put(labels, "metric_type", metric["type"])
	for prefix, v := range map[string]any{"resource.": resource["labels"], "metric.": metric["labels"], "user.": inc["policy_user_labels"]} {
		if m, ok := record(v); ok {
			for k, val := range m {
				put(labels, prefix+k, val)
			}
		}
	}
	annotations := map[string]string{}
	put(annotations, "threshold_value", inc["threshold_value"])
	put(annotations, "observed_value", inc["observed_value"])
	put(annotations, "runbook", doc["content"])
	put(annotations, "summary", inc["summary"])
	severity := str(inc["severity"])
	if gcpAbsentSeverity[strings.ToLower(strings.TrimSpace(severity))] {
		severity = "warning"
	}
	// The operator's name for the alert, not the auto-generated metric sentence.
	title := ""
	for _, k := range []string{"policy_name", "condition_name", "summary"} {
		if title = str(inc[k]); title != "" {
			break
		}
	}
	if title == "" {
		title = "Google Cloud Monitoring incident"
	}
	links := []string{}
	if u := str(inc["url"]); u != "" {
		links = append(links, u)
	}
	occurred := ""
	if status == oncall.StatusResolved {
		occurred, _ = epochToISO(inc["ended_at"])
	}
	if occurred == "" {
		occurred, _ = epochToISO(inc["started_at"])
	}
	if occurred == "" {
		occurred = isoTime(c.Now)
	}
	return []oncall.Event{Cap(oncall.Event{
		Source: "gcp", Fingerprint: incidentID, Status: status, Severity: severity, Title: title, Description: str(doc["content"]),
		Labels: labels, Annotations: annotations, Links: links, OccurredAt: occurred,
	})}, nil
}

// DeliveryID is <incident_id>:<state>.
func (gcp) DeliveryID(p any, _ Context) string {
	inc, err := incidentOf(p)
	if err != nil {
		return ""
	}
	id, state := str(inc["incident_id"]), str(inc["state"])
	if id == "" || state == "" {
		return ""
	}
	return id + ":" + state
}
