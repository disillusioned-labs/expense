package approval

import (
	"time"

	"github.com/google/uuid"
)

type CreateRuleInput struct {
	ProjectID  *uuid.UUID
	ApproverID uuid.UUID
	Step       int16
}

type UpdateRuleInput struct {
	Step int16
}

type DecideInput struct {
	Decision string
	Notes    *string
}

type ActorSnapshot struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

type Rule struct {
	ID         uuid.UUID     `json:"id"`
	ProjectID  *uuid.UUID    `json:"project_id"`
	ApproverID uuid.UUID     `json:"approver_id"`
	Approver   ActorSnapshot `json:"approver"`
	Step       int16         `json:"step"`
}

type SubmitResult struct {
	TransactionID   uuid.UUID `json:"transaction_id"`
	Status          string    `json:"transaction_status"`
	FirstApproverID uuid.UUID `json:"first_approver_id"`
}

type ApprovalStateView struct {
	ID          uuid.UUID     `json:"id"`
	Step        int16         `json:"step"`
	State       string        `json:"state"`
	Approver    ActorSnapshot `json:"approver"`
	ActivatedAt *time.Time    `json:"activated_at"`
}

type DecideResult struct {
	Approval          ApprovalStateView `json:"approval"`
	TransactionStatus string            `json:"transaction_status"`
	NextStepActivated bool              `json:"next_step_activated"`
}

type AssignedItem struct {
	ApprovalID    uuid.UUID     `json:"approval_id"`
	Step          int16         `json:"step"`
	ActivatedAt   time.Time     `json:"activated_at"`
	TransactionID uuid.UUID     `json:"transaction_id"`
	Status        string        `json:"transaction_status"`
	TotalAmount   int64         `json:"total_amount"`
	Currency      string        `json:"currency"`
	Description   *string       `json:"description"`
	Category      *string       `json:"category"`
	ProjectID     uuid.UUID     `json:"project_id"`
	ProjectName   string        `json:"project_name"`
	CreatedBy     ActorSnapshot `json:"created_by"`
	DocumentCount int64         `json:"document_count"`
	SubmittedAt   time.Time     `json:"submitted_at"`
}

// AgingItem is one pending transaction on the admin's bottleneck list: the
// oldest active approval step has been waiting longer than the cutoff.
type AgingItem struct {
	TransactionID    uuid.UUID `json:"transaction_id"`
	Description      *string   `json:"description"`
	TotalAmount      int64     `json:"total_amount"`
	Currency         string    `json:"currency"`
	ProjectID        uuid.UUID `json:"project_id"`
	ProjectName      string    `json:"project_name"`
	SubmittedAt      time.Time `json:"submitted_at"`
	WaitingSince     time.Time `json:"waiting_since"`
	DaysWaiting      int       `json:"days_waiting"`
	PendingApprovers []string  `json:"pending_approvers"`
	PendingCount     int64     `json:"pending_approver_count"`
}

// ApproverRuleRef is the rule-level payload identity relays to its client in
// the APPROVER_STILL_ASSIGNED error details (see api-contract.md).
type ApproverRuleRef struct {
	ID        uuid.UUID  `json:"id"`
	ProjectID *uuid.UUID `json:"project_id"`
	Step      int        `json:"step"`
}
