package approval

import "github.com/google/uuid"

const (
	EventApprovalRuleCreated    = "approval_rule.created"
	EventApprovalRuleUpdated    = "approval_rule.updated"
	EventApprovalRuleDeleted    = "approval_rule.deleted"
	EventApprovalRuleReassigned = "approval_rule.reassigned"
)

type ApprovalRuleCreatedEvent struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	ProjectID      *uuid.UUID `json:"project_id,omitempty"`
	ApproverID     uuid.UUID  `json:"approver_id"`
	Step           int        `json:"step"`
	ActorID        uuid.UUID  `json:"actor_id"`
}

type ApprovalRuleUpdatedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	RuleID         uuid.UUID `json:"rule_id"`
	Step           int       `json:"step"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type ApprovalRuleDeletedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	RuleID         uuid.UUID `json:"rule_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

// ApprovalRuleReassignedEvent records the D2 member-removal reassignment:
// which admin moved whose rules to whom.
type ApprovalRuleReassignedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	FromUserID     uuid.UUID `json:"from_user_id"`
	ToUserID       uuid.UUID `json:"to_user_id"`
	RuleCount      int       `json:"rule_count"`
	ActorID        uuid.UUID `json:"actor_id"`
}
