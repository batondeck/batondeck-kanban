// Package oncall is the on-call integrations' pure core: the source catalog, the sealed secret
// envelope and the webhook HMAC verification. It imports nothing of this module but the standard
// library, so the service (the tools) and the ingest route (R2.9) share one copy of each rule.
package oncall

// AuthMode is how an integration's source proves a delivery is its own.
type AuthMode string

const (
	AuthHMAC   AuthMode = "hmac"
	AuthAPIKey AuthMode = "api_key"
	AuthBasic  AuthMode = "basic"
	AuthJWT    AuthMode = "jwt"
	// AuthMTLS is reserved: the enum carries it so a config round-trips, and nothing verifies it.
	AuthMTLS AuthMode = "mtls"
)

// AuthModes are the modes, in the schema's order.
var AuthModes = []AuthMode{AuthAPIKey, AuthBasic, AuthHMAC, AuthJWT, AuthMTLS}

// Source is one telemetry source an integration can receive from (conductor oncall-sources.ts).
type Source struct {
	// ID is the wire id: an integration's source and every normalized event's.
	ID    string
	Label string
	// AuthModes are the modes the source can actually produce, best first.
	AuthModes []AuthMode
	// AuthNote is one line on why the modes are what they are.
	AuthNote string
}

// Sources is the catalog: the eight connectors' sources, in conductor's order.
var Sources = []Source{
	{ID: "generic", Label: "Generic signed JSON", AuthModes: []AuthMode{AuthHMAC},
		AuthNote: "The full BatonDeck scheme: timestamped and replay-bounded. Use it for a sender you control."},
	{ID: "grafana", Label: "Grafana", AuthModes: []AuthMode{AuthAPIKey},
		AuthNote: "Grafana cannot compute our signature, but it can send a custom header."},
	{ID: "alertmanager", Label: "Alertmanager", AuthModes: []AuthMode{AuthAPIKey},
		AuthNote: "Alertmanager cannot compute our signature, but it can send a custom header."},
	{ID: "dynatrace", Label: "Dynatrace", AuthModes: []AuthMode{AuthAPIKey},
		AuthNote: "Dynatrace cannot compute our signature, but it can send a custom header."},
	{ID: "datadog", Label: "Datadog", AuthModes: []AuthMode{AuthAPIKey},
		AuthNote: "Datadog cannot compute our signature, but it can send a custom header."},
	{ID: "gcp", Label: "Google Cloud Monitoring", AuthModes: []AuthMode{AuthBasic},
		AuthNote: "Cloud Monitoring can send HTTP Basic auth only — no custom headers, and it cannot sign. Use any username; the key is the password."},
	{ID: "pagerduty", Label: "PagerDuty", AuthModes: []AuthMode{AuthHMAC},
		AuthNote: "PagerDuty issues its OWN secret and signs the raw body with no timestamp — set externalSecret."},
	{ID: "webhook", Label: "Custom webhook (any source)", AuthModes: []AuthMode{AuthHMAC, AuthAPIKey, AuthBasic, AuthJWT},
		AuthNote: "Any of the four modes — pick what your tool can send. The payload shape is yours; you describe where the fields live."},
}

// SourceIDs are the catalog's ids, in its order.
func SourceIDs() []string {
	out := make([]string, len(Sources))
	for i, s := range Sources {
		out[i] = s.ID
	}
	return out
}

// LookupSource is the catalog entry for id.
func LookupSource(id string) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}
