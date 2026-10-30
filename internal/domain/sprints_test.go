package domain

import "testing"

// The fixed sprint table: 4×4 moves, 4 legal, the rest INVALID_TRANSITION with the allowed list.
func TestSprintTransitions(t *testing.T) {
	legal := map[[2]SprintStatus]bool{
		{SprintProposed, SprintActive}: true, {SprintProposed, SprintAbandoned}: true,
		{SprintActive, SprintClosed}: true, {SprintActive, SprintAbandoned}: true,
	}
	n := 0
	for _, from := range AllSprintStatuses() {
		for _, to := range AllSprintStatuses() {
			err := CheckSprintTransition(from, to)
			if legal[[2]SprintStatus{from, to}] {
				if err != nil {
					t.Errorf("%s → %s refused: %v", from, to, err)
				}
				n++
				continue
			}
			if e, ok := AsBatonDeckError(err); !ok || e.Code != CodeInvalidTransition || e.Details["allowed"] == nil {
				t.Errorf("%s → %s = %v, want INVALID_TRANSITION with allowed", from, to, err)
			}
		}
	}
	if n != 4 {
		t.Fatalf("%d legal sprint moves, want 4", n)
	}
	if _, err := ParseSprintStatus("OPEN"); err == nil {
		t.Fatal("OPEN parsed as a sprint status")
	}
}

func TestSprintMembership(t *testing.T) {
	base := SprintMembership{SprintID: "S-a", SprintStatus: SprintActive, Add: true, TaskID: "T-1", Exclusive: true}
	code := func(m SprintMembership) Code {
		e, ok := AsBatonDeckError(CheckSprintMembership(m))
		if !ok {
			return ""
		}
		return e.Code
	}
	cases := []struct {
		name string
		m    SprintMembership
		want Code
	}{
		{"add to an active sprint", base, ""},
		{"re-add to the same sprint", func() SprintMembership { m := base; m.TaskSprints = []string{"S-a"}; return m }(), ""},
		{"add to a closed sprint", func() SprintMembership { m := base; m.SprintStatus = SprintClosed; return m }(), CodeInvalidTransition},
		{"remove from an abandoned sprint", func() SprintMembership { m := base; m.SprintStatus = SprintAbandoned; m.Add = false; return m }(), CodeInvalidTransition},
		{"exclusive, in another sprint", func() SprintMembership { m := base; m.TaskSprints = []string{"S-b"}; return m }(), CodeConflictLocked},
		{"not exclusive, in another sprint", func() SprintMembership { m := base; m.Exclusive = false; m.TaskSprints = []string{"S-b"}; return m }(), ""},
		{"at the member cap", func() SprintMembership { m := base; m.MemberCount = SprintMaxTasks; return m }(), CodeValidation},
		{"at the sprintIds cap", func() SprintMembership {
			m := base
			m.Exclusive = false
			m.TaskSprints = []string{"1", "2", "3", "4", "5", "6", "7", "8"}
			return m
		}(), CodeValidation},
	}
	for _, c := range cases {
		if got := code(c.m); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSprintActivation(t *testing.T) {
	id := "T-9"
	done := &SprintProposal{ID: id, Status: Done, SprintProposalFor: "S-a", UpdatedBy: "bob"}
	base := SprintActivation{SprintID: "S-a", ProposalTaskID: &id, Proposal: done, ActorRole: RoleAdmin, SelfApproval: "warn"}
	by, self, err := CheckSprintActivation(base)
	if err != nil || by != "bob" || self {
		t.Fatalf("activation = %q %v %v, want bob, no self-approval", by, self, err)
	}
	code := func(a SprintActivation) Code {
		_, _, err := CheckSprintActivation(a)
		e, _ := AsBatonDeckError(err)
		if e == nil {
			return ""
		}
		return e.Code
	}
	master := base
	master.ActorRole = RoleMaster
	unbound := base
	unbound.ProposalTaskID = nil
	todo := base
	todo.Proposal = &SprintProposal{ID: id, Status: Todo, SprintProposalFor: "S-a"}
	other := base
	other.Proposal = &SprintProposal{ID: id, Status: Done, SprintProposalFor: "S-b"}
	cancelled := base
	cancelled.Proposal = &SprintProposal{ID: id, Status: Done, Cancelled: true, SprintProposalFor: "S-a"}
	enforce := base
	enforce.ActorIsCreator, enforce.SelfApproval = true, "enforce"
	for name, c := range map[string]struct {
		a    SprintActivation
		want Code
	}{
		"master": {master, CodeForbidden}, "unbound": {unbound, CodeInvalidTransition}, "not DONE": {todo, CodeInvalidTransition},
		"bound elsewhere": {other, CodeInvalidTransition}, "self under enforce": {enforce, CodeForbidden},
		"cancelled (P-208)": {cancelled, CodeInvalidTransition},
	} {
		if got := code(c.a); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	warn := base
	warn.ActorIsCreator = true
	warn.Proposal = &SprintProposal{ID: id, Status: Done, SprintProposalFor: "S-a", Reviewer: "rita", UpdatedBy: "bob"}
	if by, self, err := CheckSprintActivation(warn); err != nil || by != "rita" || !self {
		t.Fatalf("self-approval under warn = %q %v %v, want rita and a self-approval", by, self, err)
	}
}
