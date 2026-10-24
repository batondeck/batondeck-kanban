package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Grants is the capability grants service.
type Grants interface {
	// Grant is grant_capabilities: scope absent writes domain.WorkerDefaultGrant, null removes the
	// grant, an object replaces it; a member's grant narrows what their role allows and never widens
	// it, and a grant on a project admin is not enforced.
	// Tier: admin. Event: none. Audit: capabilities.granted (domain.DescribeAudit). Doorbell: none.
	Grant(ctx context.Context, caller domain.Caller, req GrantCapabilitiesReq) (GrantCapabilitiesResp, error)
}

// GrantCapabilitiesReq is grant_capabilities's input.
type GrantCapabilitiesReq struct {
	IdentityID string          `json:"identityId"`
	ProjectID  string          `json:"projectId"`
	Scope      Nullable[Grant] `json:"scope,omitzero"`
}

// GrantCapabilitiesResp is grant_capabilities's output: the member's grant after the call, null when
// it holds none.
type GrantCapabilitiesResp struct {
	Grant      *Grant `json:"grant"`
	IdentityID string `json:"identityId"`
}
