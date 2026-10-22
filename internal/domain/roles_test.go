package domain

import "testing"

// Conductor has no unit table for the ladder (ROLE_RANK, src/domain/types.ts:52, and TIER_MIN_RANK,
// src/authz/manifest.ts); these pin it.

func TestParseRole(t *testing.T) {
	for _, r := range AllRoles() {
		if got, err := ParseRole(string(r)); err != nil || got != r {
			t.Errorf("ParseRole(%q) = %q, %v", r, got, err)
		}
	}
	// agent is conductor's retired role; read/write are tiers, not roles.
	for _, s := range []string{"", "agent", "read", "write", "Admin", "owner"} {
		_, err := ParseRole(s)
		if e, ok := AsBatonDeckError(err); !ok || e.Code != CodeValidation {
			t.Errorf("ParseRole(%q) = %v, want VALIDATION", s, err)
		}
	}
}

func TestRoleRankLadder(t *testing.T) {
	for i, r := range AllRoles() {
		if RoleRank(r) != i {
			t.Errorf("RoleRank(%s) = %d, want %d", r, RoleRank(r), i)
		}
	}
	if RoleRank("agent") != -1 {
		t.Fatal("an unknown role ranks -1")
	}
}

func TestRoleMeetsTier(t *testing.T) {
	min := map[Tier]Role{TierRead: RoleViewer, TierWrite: RoleWorker, TierMaster: RoleMaster, TierStructural: RoleMember, TierAdmin: RoleAdmin}
	for tier, floor := range min {
		if TierMinRole(tier) != floor {
			t.Errorf("TierMinRole(%s) = %s, want %s", tier, TierMinRole(tier), floor)
		}
		for _, r := range AllRoles() {
			want := RoleRank(r) >= RoleRank(floor)
			if got := RoleMeetsTier(r, tier); got != want {
				t.Errorf("RoleMeetsTier(%s, %s) = %v, want %v", r, tier, got, want)
			}
		}
	}
	if RoleMeetsTier("agent", TierRead) || RoleMeetsTier(RoleAdmin, "owner") {
		t.Fatal("an unknown role or tier meets nothing")
	}
	// Un-poison needs master: a worker does not meet it, a master does.
	if RoleMeetsTier(RoleWorker, TierMaster) || !RoleMeetsTier(RoleMaster, TierMaster) {
		t.Fatal("the master tier")
	}
}
