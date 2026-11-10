package domain

import (
	"fmt"
	"slices"
)

// SprintStatus is a sprint's lifecycle state (conductor's domain/sprints.ts). Its table is fixed, not
// a project setting.
type SprintStatus string

const (
	SprintProposed  SprintStatus = "PROPOSED"
	SprintActive    SprintStatus = "ACTIVE"
	SprintClosed    SprintStatus = "CLOSED"
	SprintAbandoned SprintStatus = "ABANDONED"
)

// AllSprintStatuses returns the four sprint statuses in lifecycle order.
func AllSprintStatuses() []SprintStatus {
	return []SprintStatus{SprintProposed, SprintActive, SprintClosed, SprintAbandoned}
}

// SprintMaxTasks caps a sprint's members below get_sprint's 200-task member page, so its derived
// counts are exact; SprintIDsMax bounds a task's sprintIds even when membership is not exclusive.
const (
	SprintMaxTasks = 100
	SprintIDsMax   = 8
)

var sprintTransitions = map[SprintStatus][]SprintStatus{
	SprintProposed:  {SprintActive, SprintAbandoned},
	SprintActive:    {SprintClosed, SprintAbandoned},
	SprintClosed:    {},
	SprintAbandoned: {},
}

// ParseSprintStatus is s as a SprintStatus, or VALIDATION.
func ParseSprintStatus(s string) (SprintStatus, error) {
	if _, ok := sprintTransitions[SprintStatus(s)]; ok {
		return SprintStatus(s), nil
	}
	return "", Validation("unknown sprint status: "+s, map[string]any{"status": s})
}

// SprintAllowedNext is the sprint statuses one legal move from from reaches.
func SprintAllowedNext(from SprintStatus) []SprintStatus {
	return append([]SprintStatus{}, sprintTransitions[from]...)
}

// CheckSprintTransition is nil when from → to is a legal sprint move, INVALID_TRANSITION with the
// allowed next statuses otherwise. A same-status patch is no transition and never reaches here.
func CheckSprintTransition(from, to SprintStatus) error {
	for _, s := range sprintTransitions[from] {
		if s == to {
			return nil
		}
	}
	return NewError(CodeInvalidTransition, fmt.Sprintf("Illegal sprint transition %s → %s.", from, to),
		map[string]any{"from": from, "to": to, "allowed": SprintAllowedNext(from)})
}

// SprintMembership is what CheckSprintMembership judges: the sprint's id, status and member count,
// the action, the task and its current sprintIds, those of them that have ended (CLOSED or
// ABANDONED), and whether membership is exclusive.
type SprintMembership struct {
	SprintID     string
	SprintStatus SprintStatus
	MemberCount  int64
	Add          bool
	TaskID       string
	TaskSprints  []string
	// EndedSprints is the ids of TaskSprints whose sprint is CLOSED or ABANDONED: the task keeps
	// them for history (a membership change on an ended sprint is refused), and they hold it out of
	// no other sprint.
	EndedSprints []string
	Exclusive    bool
}

// CheckSprintMembership is conductor's assertSprintMembershipChange: a member is added or removed
// only while the sprint is PROPOSED or ACTIVE (INVALID_TRANSITION); an add under exclusivity to a
// task already in another sprint that has not ended is CONFLICT_LOCKED, so the unfinished member of
// a CLOSED or ABANDONED sprint is carried to a successor; an add past SprintMaxTasks members, or to a
// task already in SprintIDsMax sprints (ended ones too), is VALIDATION.
func CheckSprintMembership(m SprintMembership) error {
	if m.SprintStatus != SprintProposed && m.SprintStatus != SprintActive {
		return NewError(CodeInvalidTransition,
			fmt.Sprintf("Sprint %s is %s — membership changes are legal only while PROPOSED or ACTIVE", m.SprintID, m.SprintStatus),
			map[string]any{"sprintId": m.SprintID, "sprintStatus": m.SprintStatus})
	}
	if !m.Add {
		return nil
	}
	in := false
	var holding []string
	for _, s := range m.TaskSprints {
		in = in || s == m.SprintID
		if s != m.SprintID && !slices.Contains(m.EndedSprints, s) {
			holding = append(holding, s)
		}
	}
	if m.Exclusive && len(holding) > 0 && !in {
		return NewError(CodeConflictLocked, fmt.Sprintf("Task %s is already in sprint %s (SPRINT_EXCLUSIVE)", m.TaskID, holding[0]),
			map[string]any{"currentSprintIds": holding})
	}
	if m.MemberCount >= SprintMaxTasks {
		return Validation(fmt.Sprintf("Sprint %s is at SPRINT_MAX_TASKS (%d) — a sprint is a bounded objective; open a successor sprint", m.SprintID, SprintMaxTasks),
			map[string]any{"sprintId": m.SprintID, "memberCount": m.MemberCount})
	}
	if !in && len(m.TaskSprints) >= SprintIDsMax {
		return Validation(fmt.Sprintf("Task %s is already in %d sprints (SPRINT_IDS_MAX)", m.TaskID, SprintIDsMax),
			map[string]any{"sprintIds": append([]string{}, m.TaskSprints...)})
	}
	return nil
}

// SprintActivation is what CheckSprintActivation judges: the sprint, its bound proposal task (nil
// when unset or missing), the actor's project role and whether it created the sprint, and the
// project's selfApprovalPolicy ("off", "warn" or "enforce").
type SprintActivation struct {
	SprintID       string
	ProposalTaskID *string
	CreatedBy      string
	Proposal       *SprintProposal
	ActorRole      Role
	ActorIsCreator bool
	SelfApproval   string
}

// SprintProposal is the proposal task as the activation gate reads it: its status, whether it
// carries the cancelled label, the customFields.sprintProposalFor it carries ("" when absent), its
// review's reviewer ("" when none) and its updatedBy.
type SprintProposal struct {
	ID                string
	Status            Status
	Cancelled         bool
	SprintProposalFor string
	Reviewer          string
	UpdatedBy         string
}

// CheckSprintActivation is conductor's assertSprintActivation, the PROPOSED → ACTIVE gates in order:
// the admin role (FORBIDDEN), a bound DONE proposal task that was not cancelled (P-208) and whose
// customFields.sprintProposalFor is the sprint (INVALID_TRANSITION), and self-approval under "enforce" (FORBIDDEN). It returns approvedBy
// (the proposal's reviewer, else its updatedBy) and whether a sprint.self_approved event is due
// (self-approval under "warn").
func CheckSprintActivation(a SprintActivation) (approvedBy string, selfApproved bool, err error) {
	if a.ActorRole != RoleAdmin {
		return "", false, NewError(CodeForbidden, "Activating a sprint requires the project admin role",
			map[string]any{"requiredRole": RoleAdmin, "actorRole": a.ActorRole})
	}
	refuse := func(reason string) error {
		var bound any
		if a.ProposalTaskID != nil {
			bound = *a.ProposalTaskID
		}
		return NewError(CodeInvalidTransition, fmt.Sprintf("Sprint %s cannot activate: %s", a.SprintID, reason),
			map[string]any{"reason": reason, "proposalTaskId": bound})
	}
	p := a.Proposal
	switch {
	case a.ProposalTaskID == nil || p == nil:
		return "", false, refuse("no DONE proposal task is bound (proposalTaskId)")
	case p.Status != Done:
		return "", false, refuse(fmt.Sprintf("proposal task %s is %s, not DONE", p.ID, p.Status))
	case p.Cancelled:
		return "", false, refuse(fmt.Sprintf("proposal task %s was cancelled, not delivered", p.ID))
	case p.SprintProposalFor != a.SprintID:
		return "", false, refuse(fmt.Sprintf("proposal task %s is not bound to this sprint (customFields.sprintProposalFor)", p.ID))
	}
	if a.ActorIsCreator && a.SelfApproval == "enforce" {
		return "", false, NewError(CodeForbidden,
			fmt.Sprintf("Sprint %s was proposed by its activator and selfApprovalPolicy is 'enforce'", a.SprintID),
			map[string]any{"sprintId": a.SprintID, "createdBy": a.CreatedBy})
	}
	approvedBy = p.UpdatedBy
	if p.Reviewer != "" {
		approvedBy = p.Reviewer
	}
	return approvedBy, a.ActorIsCreator && a.SelfApproval == "warn", nil
}
