package connectors

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// generic is the BatonDeck-native signed webhook (conductor generic.ts): the payload is the event
// shape less source, validated strictly, an unknown top-level key refused by name (the one
// connector whose schema is ours, so a typo is an error the sender sees).
type generic struct{}

var genericKeys = map[string]bool{"fingerprint": true, "status": true, "severity": true, "title": true, "description": true,
	"labels": true, "annotations": true, "links": true, "upstreamAssignee": true, "occurredAt": true, "raw": true, "deliveryId": true}

func requireStringMap(v any, field string) (map[string]string, error) {
	if v == nil {
		return map[string]string{}, nil
	}
	m, ok := record(v)
	if !ok {
		return nil, fmt.Errorf("generic payload: %s must be an object of strings", field)
	}
	out := map[string]string{}
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("generic payload: %s.%s must be a string", field, k)
		}
		out[k] = s
	}
	return out, nil
}

func (generic) Events(p any, c Context) ([]oncall.Event, error) {
	m, ok := record(p)
	if !ok {
		return nil, errors.New("unrecognizable generic payload: expected a JSON object")
	}
	var unknown []string
	for k := range m {
		if !genericKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("generic payload: unknown keys: %s", strings.Join(unknown, ", "))
	}
	e := oncall.Event{Source: "generic", Fingerprint: str(m["fingerprint"]), Severity: str(m["severity"]), Title: str(m["title"])}
	switch {
	case e.Fingerprint == "":
		return nil, errors.New("generic payload: fingerprint (non-empty string) is required")
	case !inStatuses(m["status"]):
		return nil, errors.New("generic payload: status must be one of 'firing' | 'resolved' | 'test'")
	case e.Severity == "":
		return nil, errors.New("generic payload: severity (non-empty string) is required")
	case e.Title == "":
		return nil, errors.New("generic payload: title (non-empty string) is required")
	}
	e.Status = m["status"].(string)
	if d, ok := m["description"]; ok {
		s, isStr := d.(string)
		if !isStr {
			return nil, errors.New("generic payload: description must be a string")
		}
		e.Description = s
	}
	var err error
	if e.Labels, err = requireStringMap(m["labels"], "labels"); err != nil {
		return nil, err
	}
	if e.Annotations, err = requireStringMap(m["annotations"], "annotations"); err != nil {
		return nil, err
	}
	e.Links = []string{}
	if l, ok := m["links"]; ok && l != nil {
		arr, isArr := l.([]any)
		if !isArr {
			return nil, errors.New("generic payload: links must be an array of strings")
		}
		for _, x := range arr {
			s, isStr := x.(string)
			if !isStr {
				return nil, errors.New("generic payload: links must be an array of strings")
			}
			e.Links = append(e.Links, s)
		}
	}
	for _, f := range []string{"upstreamAssignee", "deliveryId"} {
		if v, ok := m[f]; ok {
			if _, isStr := v.(string); !isStr {
				return nil, fmt.Errorf("generic payload: %s must be a string", f)
			}
		}
	}
	e.UpstreamAssignee = str(m["upstreamAssignee"])
	e.OccurredAt = isoTime(c.Now)
	if v, ok := m["occurredAt"]; ok {
		t, ok := realTime(v)
		if !ok {
			return nil, errors.New("generic payload: occurredAt must be an ISO timestamp")
		}
		e.OccurredAt = t
	}
	return []oncall.Event{Cap(e)}, nil
}

func inStatuses(v any) bool {
	s, _ := v.(string)
	return s == oncall.StatusFiring || s == oncall.StatusResolved || s == oncall.StatusTest
}

// DeliveryID is the payload's optional top-level deliveryId.
func (generic) DeliveryID(p any, _ Context) string {
	m, _ := record(p)
	return str(m["deliveryId"])
}
