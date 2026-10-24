// Package service is the application layer (ADR-0003, layer 2): each rule lives here once. The
// transports (internal/mcp, internal/api, internal/wake) call these interfaces and nothing below
// them; the implementations compose internal/store and internal/domain.
//
// Every method is (ctx, domain.Caller, Req) (Resp, error), and an error is a *domain.BatonDeckError
// carrying one of domain.AllCodes. Every mutation runs inside service.mutate: one transaction that
// loads, checks the expected version (STALE), applies, bumps, appends the event, writes the audit
// row (domain.DescribeAudit) and the change-log row with its token scope (bus-visible after commit),
// and stores or replays the idempotency key on a create. Membership, the role tier and the
// capability grant (domain.CheckGrant) are checked by service.authorize before it; a project the
// caller is not a member of is FORBIDDEN. Each method's comment names its tier, its event, its audit
// descriptor and its doorbell kind.
//
// The DTOs are the tools' schemas (the structs the MCP layer registers), so contract/expected.json
// guards them: contract/dto_test.go infers each pair with jsonschema-go and compares it exactly. The
// Go-side rules: a JSON number is float64 and a JSON integer int64; an optional property is a value
// with omitzero; a nullable one is a pointer; an enum is a named string type; custom fields are
// map[string]FieldValue. A required slice in a response is never nil (it is [] on the wire; null
// fails go-sdk's output validation): servicefake refuses a configured response that breaks it, and
// the real implementations must hold it too.
package service

// Services is the bundle a transport is built from. One value swaps servicefake for the real
// service over memstore or over either engine (ADR-0003 B3; R1.18).
type Services struct {
	Projects  Projects
	Boards    Boards
	Tasks     Tasks
	Lifecycle Lifecycle
	Context   Context
	Media     Media
	Artifacts Artifacts
	Comments  Comments
	FollowUps FollowUps
	Chronicle Chronicle
	Feed      Feed
	Sessions  Sessions
	ToolCalls ToolCalls
	Oncall    Oncall
	Runs      Runs
	Sprints   Sprints
	Me        Me
	Audit     Audit
}
