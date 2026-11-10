package oncall

// Event is a normalized alert (conductor AlertEvent less raw): what a connector makes of a verified
// delivery, what a delivery row keeps, and what replay_delivery ingests again. OccurredAt is RFC 3339.
type Event struct {
	Source      string            `json:"source"`
	Fingerprint string            `json:"fingerprint"`
	Status      string            `json:"status"` // firing, resolved or test
	Severity    string            `json:"severity"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Links       []string          `json:"links"`
	// UpstreamAssignee is the source's assignee (PagerDuty's user id), the upstream routing input.
	UpstreamAssignee string `json:"upstreamAssignee,omitempty"`
	OccurredAt       string `json:"occurredAt"`
	// DeliveryKey is the source's id for this one event's notification when a delivery carries
	// several and its own id names none of them (Alertmanager's groupKey names the group, the same on
	// every notification of it): the delivery id is <DeliveryKey>#<fingerprint>. "" is none. Never
	// stored: a delivery row is under its delivery id.
	DeliveryKey string `json:"-"`
}

// The event statuses.
const (
	StatusFiring   = "firing"
	StatusResolved = "resolved"
	StatusTest     = "test"
)

// Action is what ingesting one event does (conductor decideIngestAction, incident.ts:167-182).
type Action string

const (
	ActionCreate   Action = "create"
	ActionAppend   Action = "append"
	ActionAnnotate Action = "annotate"
	ActionClose    Action = "close"
	ActionRecord   Action = "record"
)

// Decide is the lifecycle table: lifecycle is the integration's (correlate, autoclose, per_event),
// open whether the event's fingerprint correlates to an open incident ticket, untouched whether that
// ticket is untouched (conductor isUntouched). A test event always files a fresh ticket.
func Decide(lifecycle, status string, open, untouched bool) Action {
	switch {
	case status == StatusTest:
		return ActionCreate
	case lifecycle == "per_event":
		if status == StatusFiring {
			return ActionCreate
		}
		return ActionRecord
	case status == StatusFiring && open:
		return ActionAppend
	case status == StatusFiring:
		return ActionCreate
	case !open:
		return ActionRecord
	case lifecycle == "autoclose" && untouched:
		return ActionClose
	}
	return ActionAnnotate
}
