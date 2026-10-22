package domain

// Caller is the authenticated principal every service call carries (ADR-0003).
type Caller struct {
	Identity       string
	Agent          string
	Role           string
	WorkspaceAdmin bool
	TokenScope     string
}
