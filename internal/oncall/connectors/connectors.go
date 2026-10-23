// Package connectors turns one verified webhook body into normalized events (conductor
// functions/src/oncall/connectors): one pure module per source of the catalog (oncall.Sources),
// payload in, events out, no I/O and no auth. A delivery may carry several alerts (Grafana and
// Alertmanager group them), so a connector returns a slice; an unrecognizable payload is an error
// whose message names the field, which the ingest records on the delivery log.
package connectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

// Context is what a connector knows beyond the payload: the integration's mapping (read by the
// webhook connector alone), the body's hash (the delivery id's fallback and the mapping's
// $body_hash) and the clock, injected so a connector stays pure.
type Context struct {
	Mapping  *Mapping
	BodyHash string
	Now      time.Time
}

// Connector is one source's mapping.
type Connector interface {
	// Events maps one decoded payload to zero or more normalized events, every one capped (Cap).
	Events(payload any, c Context) ([]oncall.Event, error)
	// DeliveryID is the source's own per-delivery id, "" when it has none (the caller hashes the
	// body instead).
	DeliveryID(payload any, c Context) string
}

// registry is keyed by the catalog's ids; TestEverySourceHasAConnector holds the two lists equal.
var registry = map[string]Connector{
	"generic":      generic{},
	"grafana":      grafana{},
	"alertmanager": alertmanager{},
	"dynatrace":    dynatrace{},
	"datadog":      datadog{},
	"pagerduty":    pagerduty{},
	"gcp":          gcp{},
	"webhook":      webhook{},
}

// For is the connector of source.
func For(source string) (Connector, bool) {
	c, ok := registry[source]
	return c, ok
}

// Decode parses a body as one JSON value, numbers kept as json.Number so an id keeps its digits.
func Decode(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("the body holds more than one JSON value")
	}
	return v, nil
}
