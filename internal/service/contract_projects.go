package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Projects is the projects, settings and membership service.
type Projects interface {
	// Create is create_project.
	// Tier: any authenticated caller (not tenant-scoped); the caller becomes the project's admin.
	// Event: project.created. Audit: none. Doorbell: none. Replays on idempotencyKey.
	Create(ctx context.Context, caller domain.Caller, req CreateProjectReq) (CreateProjectResp, error)

	// Get is get_project.
	// Tier: read. Writes nothing.
	Get(ctx context.Context, caller domain.Caller, req GetProjectReq) (GetProjectResp, error)

	// List is list_projects.
	// Tier: none (the caller's own memberships; a workspace admin sees all). Writes nothing.
	List(ctx context.Context, caller domain.Caller, req ListProjectsReq) (ListProjectsResp, error)

	// UpdateSettings is update_settings.
	// Tier: admin. Event: none. Audit: settings.updated (domain.DescribeAudit). Doorbell: none.
	UpdateSettings(ctx context.Context, caller domain.Caller, req UpdateSettingsReq) (UpdateSettingsResp, error)

	// Delete is delete_project.
	// Tier: admin. Event: none (the project's logs go with it). Audit: project.deleted. Doorbell: none.
	Delete(ctx context.Context, caller domain.Caller, req DeleteProjectReq) (DeleteProjectResp, error)

	// AddMember is add_member.
	// Tier: admin; upserts the role. Event: none. Audit: member.added. Doorbell: none.
	AddMember(ctx context.Context, caller domain.Caller, req AddMemberReq) (AddMemberResp, error)

	// RemoveMember is remove_member.
	// Tier: admin. Event: none. Audit: member.removed. Doorbell: none.
	RemoveMember(ctx context.Context, caller domain.Caller, req RemoveMemberReq) (RemoveMemberResp, error)
}

// CreateProjectReq is create_project's input.
type CreateProjectReq struct {
	Name           string `json:"name"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
}

// CreateProjectResp is create_project's output.
type CreateProjectResp struct {
	ProjectID string `json:"projectId"`
}

// GetProjectReq is get_project's input.
type GetProjectReq struct {
	ProjectID string `json:"projectId"`
}

// GetProjectResp is get_project's output.
type GetProjectResp struct {
	MemberDisplay map[string]string `json:"memberDisplay"`
	Project       Project           `json:"project"`
}

// ListProjectsReq is list_projects's input.
type ListProjectsReq struct {
	All bool `json:"all,omitzero"`
}

// ListProjectsResp is list_projects's output.
type ListProjectsResp struct {
	Projects []ProjectListing `json:"projects"`
}

// UpdateSettingsReq is update_settings's input.
type UpdateSettingsReq struct {
	ProjectID          string                `json:"projectId"`
	AgingCeilingMs     int64                 `json:"agingCeilingMs,omitzero"`
	AnomalyPolicy      AnomalyPolicyPatch    `json:"anomalyPolicy,omitzero"`
	ArtifactPolicy     PolicyMode            `json:"artifactPolicy,omitzero"`
	CapabilityAliases  map[string]string     `json:"capabilityAliases,omitzero"`
	CheckpointPolicy   PolicyMode            `json:"checkpointPolicy,omitzero"`
	Color              *string               `json:"color,omitzero"`
	DefaultReviewer    *string               `json:"defaultReviewer,omitzero"`
	MaxRunsPerTask     int64                 `json:"maxRunsPerTask,omitzero"`
	MaxTicketTokens    int64                 `json:"maxTicketTokens,omitzero"`
	QuotaEnabled       bool                  `json:"quotaEnabled,omitzero"`
	RetryPolicy        RetryPolicyPatch      `json:"retryPolicy,omitzero"`
	ReviewEnabled      bool                  `json:"reviewEnabled,omitzero"`
	SelectionWeights   SelectionWeightsPatch `json:"selectionWeights,omitzero"`
	SelfApprovalPolicy PolicyMode            `json:"selfApprovalPolicy,omitzero"`
	TokenPolicy        PolicyMode            `json:"tokenPolicy,omitzero"`
	TxnCap             *int64                `json:"txnCap,omitzero"`
}

// UpdateSettingsResp is update_settings's output.
type UpdateSettingsResp struct {
	Settings Settings `json:"settings"`
}

// DeleteProjectReq is delete_project's input.
type DeleteProjectReq struct {
	ProjectID string `json:"projectId"`
}

// DeleteProjectResp is delete_project's output.
type DeleteProjectResp struct {
	Ok True `json:"ok"`
}

// AddMemberReq is add_member's input.
type AddMemberReq struct {
	IdentityID string      `json:"identityId"`
	ProjectID  string      `json:"projectId"`
	Role       domain.Role `json:"role"`
}

// AddMemberResp is add_member's output.
type AddMemberResp struct {
	Members map[string]domain.Role `json:"members"`
}

// RemoveMemberReq is remove_member's input.
type RemoveMemberReq struct {
	IdentityID string `json:"identityId"`
	ProjectID  string `json:"projectId"`
}

// RemoveMemberResp is remove_member's output.
type RemoveMemberResp struct {
	Ok True `json:"ok"`
}

// Project is get_project's output project, first seen there.
type Project struct {
	CreatedAt string                 `json:"createdAt"`
	CreatedBy string                 `json:"createdBy"`
	ID        string                 `json:"id"`
	Members   map[string]domain.Role `json:"members"`
	Name      string                 `json:"name"`
	Settings  Settings               `json:"settings"`
	Grants    map[string]Grant       `json:"grants,omitzero"`
	TenantID  *string                `json:"tenantId,omitzero"`
}

// Settings is get_project's output project.settings, first seen there.
type Settings struct {
	QuotaEnabled       bool              `json:"quotaEnabled"`
	ReviewEnabled      bool              `json:"reviewEnabled"`
	TxnCap             *float64          `json:"txnCap"`
	AgingCeilingMs     float64           `json:"agingCeilingMs,omitzero"`
	AnomalyPolicy      AnomalyPolicy     `json:"anomalyPolicy,omitzero"`
	ArtifactPolicy     PolicyMode        `json:"artifactPolicy,omitzero"`
	CapabilityAliases  map[string]string `json:"capabilityAliases,omitzero"`
	CheckpointPolicy   PolicyMode        `json:"checkpointPolicy,omitzero"`
	Color              string            `json:"color,omitzero"`
	DefaultReviewer    *string           `json:"defaultReviewer,omitzero"`
	MaxRunsPerTask     int64             `json:"maxRunsPerTask,omitzero"`
	MaxTicketTokens    int64             `json:"maxTicketTokens,omitzero"`
	RetryPolicy        RetryPolicy       `json:"retryPolicy,omitzero"`
	SelectionWeights   SelectionWeights  `json:"selectionWeights,omitzero"`
	SelfApprovalPolicy PolicyMode        `json:"selfApprovalPolicy,omitzero"`
	TokenPolicy        PolicyMode        `json:"tokenPolicy,omitzero"`
}

// AnomalyPolicy is get_project's output project.settings.anomalyPolicy, first seen there.
type AnomalyPolicy struct {
	ClaimThrashCount  int64 `json:"claimThrashCount,omitzero"`
	ReworkLoopCount   int64 `json:"reworkLoopCount,omitzero"`
	StaleInProgressMs int64 `json:"staleInProgressMs,omitzero"`
	StarvedUrgentMs   int64 `json:"starvedUrgentMs,omitzero"`
	WindowSec         int64 `json:"windowSec,omitzero"`
	WIPJamMinTasks    int64 `json:"wipJamMinTasks,omitzero"`
}

// RetryPolicy is get_project's output project.settings.retryPolicy, first seen there.
type RetryPolicy struct {
	BaseDelayMs         int64   `json:"baseDelayMs"`
	Jitter              float64 `json:"jitter"`
	MaxDelayMs          int64   `json:"maxDelayMs"`
	PoisonAfterAttempts int64   `json:"poisonAfterAttempts"`
}

// SelectionWeights is get_project's output project.settings.selectionWeights, first seen there.
type SelectionWeights struct {
	Aging      float64 `json:"aging"`
	Capability float64 `json:"capability"`
	Deadline   float64 `json:"deadline"`
	Priority   float64 `json:"priority"`
	Readiness  float64 `json:"readiness"`
}

// Grant is get_project's output project.grants{}, first seen there.
type Grant struct {
	Boards       []string             `json:"boards,omitzero"`
	Deny         []string             `json:"deny,omitzero"`
	Labels       []string             `json:"labels,omitzero"`
	MaxPriority  domain.Priority      `json:"maxPriority,omitzero"`
	MemoryScopes []domain.MemoryScope `json:"memoryScopes,omitzero"`
	Statuses     []domain.Status      `json:"statuses,omitzero"`
	Tools        []string             `json:"tools,omitzero"`
}

// ProjectListing is list_projects's output projects[], first seen there.
type ProjectListing struct {
	Color         *string                 `json:"color"`
	MemberDisplay *map[string]string      `json:"memberDisplay"`
	Members       *map[string]domain.Role `json:"members"`
	Name          string                  `json:"name"`
	Owner         *string                 `json:"owner"`
	OwnerDisplay  *string                 `json:"ownerDisplay"`
	ProjectID     string                  `json:"projectId"`
	QuotaEnabled  bool                    `json:"quotaEnabled"`
	Role          domain.Role             `json:"role"`
	TxnCap        *float64                `json:"txnCap"`
}

// AnomalyPolicyPatch is update_settings's input anomalyPolicy, first seen there. It is an open object: conductor's schema allows properties it does not name.
type AnomalyPolicyPatch struct {
	ClaimThrashCount  int64 `json:"claimThrashCount,omitzero"`
	ReworkLoopCount   int64 `json:"reworkLoopCount,omitzero"`
	StaleInProgressMs int64 `json:"staleInProgressMs,omitzero"`
	StarvedUrgentMs   int64 `json:"starvedUrgentMs,omitzero"`
	WindowSec         int64 `json:"windowSec,omitzero"`
	WIPJamMinTasks    int64 `json:"wipJamMinTasks,omitzero"`
}

// RetryPolicyPatch is update_settings's input retryPolicy, first seen there. It is an open object: conductor's schema allows properties it does not name.
type RetryPolicyPatch struct {
	BaseDelayMs         int64   `json:"baseDelayMs"`
	Jitter              float64 `json:"jitter"`
	MaxDelayMs          int64   `json:"maxDelayMs"`
	PoisonAfterAttempts int64   `json:"poisonAfterAttempts"`
}

// SelectionWeightsPatch is update_settings's input selectionWeights, first seen there. It is an open object: conductor's schema allows properties it does not name.
type SelectionWeightsPatch struct {
	Aging      float64 `json:"aging"`
	Capability float64 `json:"capability"`
	Deadline   float64 `json:"deadline"`
	Priority   float64 `json:"priority"`
	Readiness  float64 `json:"readiness"`
}

// PolicyMode is an artifact, checkpoint, self-approval or token policy.
type PolicyMode string

// PolicyModeValues are PolicyMode's values, in the schema's order.
var PolicyModeValues = []PolicyMode{"enforce", "off", "warn"}

// True is a boolean that is always true: the ok of a deletion's answer.
type True bool
