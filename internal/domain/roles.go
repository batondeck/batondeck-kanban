package domain

import "fmt"

// Role is a member's persona in a project, ordered by capability. The names are conductor's and are
// on the wire (add_member's role enum):
//
//	viewer  reads only.
//	worker  pulls work, does it, reports on it: claims, the lease-guarded outcomes, annotations.
//	master  plans and supervises: a worker's surface plus creating, moving and wiring tasks, adding
//	        boards and columns, and deleting Chronicle pages.
//	member  a human teammate: a master plus structural board edits.
//	admin   the project admin: a member plus membership and settings.
//
// Workspace admin is not a role: it is a property of the account (the first local account, and those
// it adds), and the tools that need it say so in their posture's AdminOnly.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleWorker Role = "worker"
	RoleMaster Role = "master"
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
)

// AllRoles returns the roles in rank order.
func AllRoles() []Role {
	return []Role{RoleViewer, RoleWorker, RoleMaster, RoleMember, RoleAdmin}
}

var roleRank = map[Role]int{RoleViewer: 0, RoleWorker: 1, RoleMaster: 2, RoleMember: 3, RoleAdmin: 4}

// ParseRole accepts exactly the five roles; anything else is VALIDATION.
func ParseRole(s string) (Role, error) {
	if _, ok := roleRank[Role(s)]; ok {
		return Role(s), nil
	}
	return "", Validation(fmt.Sprintf("unknown role %q", s), map[string]any{"role": s, "allowed": AllRoles()})
}

// RoleRank is the role's place on the ladder; an unknown role ranks -1 and so passes no guard.
func RoleRank(r Role) int {
	if n, ok := roleRank[r]; ok {
		return n
	}
	return -1
}

// Tier is the rank a tool needs: read, write and master are the first three rungs of the ladder,
// structural is a member's, admin a project admin's.
type Tier string

const (
	TierRead       Tier = "read"
	TierWrite      Tier = "write"
	TierMaster     Tier = "master"
	TierStructural Tier = "structural"
	TierAdmin      Tier = "admin"
)

var tierMinRole = map[Tier]Role{
	TierRead: RoleViewer, TierWrite: RoleWorker, TierMaster: RoleMaster, TierStructural: RoleMember, TierAdmin: RoleAdmin,
}

// TierMinRole is the lowest role a tier admits, "" for a tier that does not exist.
func TierMinRole(t Tier) Role {
	return tierMinRole[t]
}

// RoleMeetsTier reports whether r ranks at or above the tier's minimum. An unknown role or tier fails.
func RoleMeetsTier(r Role, t Tier) bool {
	min, ok := tierMinRole[t]
	return ok && RoleRank(r) >= RoleRank(min)
}
