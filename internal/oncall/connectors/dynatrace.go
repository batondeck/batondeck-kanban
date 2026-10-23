package connectors

import (
	"errors"
	"sort"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// dynatrace is Dynatrace's problem-notification custom integration (conductor dynatrace.ts), in
// either the documented template ({problemId, state, problemTitle, severityLevel, …}) or
// Dynatrace's default placeholder names ({PID, State, ProblemTitle, ProblemSeverity, …}).
type dynatrace struct{}

// pick is the first of names present, an exact key first, then any key equal ignoring case, in key
// order so a payload carrying both spellings reads the same every time.
func pick(m map[string]any, names ...string) any {
	for _, n := range names {
		if v, ok := m[n]; ok {
			return v
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, n := range names {
			if strings.EqualFold(k, n) {
				return m[k]
			}
		}
	}
	return nil
}

func (dynatrace) Events(p any, c Context) ([]oncall.Event, error) {
	m, ok := record(p)
	if !ok {
		return nil, errors.New("unrecognizable dynatrace payload: expected a JSON object")
	}
	fingerprint := idString(pick(m, "problemId", "PID", "ProblemID"))
	if fingerprint == "" {
		return nil, errors.New("unrecognizable dynatrace payload: missing problemId/PID")
	}
	state := strings.ToUpper(str(pick(m, "state", "State")))
	severity := lower(str(pick(m, "severityLevel", "ProblemSeverity")))
	if severity == "" {
		severity = "warning"
	}
	title := str(pick(m, "problemTitle", "ProblemTitle"))
	if title == "" {
		title = "Dynatrace problem"
	}
	annotations := map[string]string{}
	if state != "" {
		annotations["dynatrace_state"] = state
	}
	if impact := str(pick(m, "problemImpact", "ProblemImpact")); impact != "" {
		annotations["dynatrace_impact"] = impact
	}
	if impacted, ok := pick(m, "impactedEntities", "ImpactedEntities").([]any); ok {
		var names []string
		for _, e := range impacted {
			if r, ok := record(e); ok {
				if n := str(r["name"]); n != "" {
					names = append(names, n)
				}
			} else if s := str(e); s != "" {
				names = append(names, s)
			}
		}
		if len(names) > 0 {
			annotations["impacted_entities"] = strings.Join(names, ", ")
		}
	}
	links := []string{}
	if u := str(pick(m, "problemUrl", "ProblemURL")); u != "" {
		links = append(links, u)
	}
	status := oncall.StatusFiring
	if state == "RESOLVED" || state == "CLOSED" {
		status = oncall.StatusResolved
	}
	return []oncall.Event{Cap(oncall.Event{
		Source: "dynatrace", Fingerprint: fingerprint, Status: status, Severity: severity, Title: title,
		Description: str(pick(m, "problemDetailsText", "ProblemDetailsText")), Labels: map[string]string{}, Annotations: annotations,
		Links: links, OccurredAt: timeOrNow(pick(m, "occurredAt", "Timestamp"), c.Now),
	})}, nil
}

// DeliveryID is none: a notification carries no per-delivery id.
func (dynatrace) DeliveryID(any, Context) string { return "" }
