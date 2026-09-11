package approval

import (
	"time"

	"github.com/google/uuid"

	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
)

type ActorResponse struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

func toActorResponse(a approvalservice.ActorSnapshot) ActorResponse {
	return ActorResponse{ID: a.ID, Name: a.Name, Email: a.Email, Role: a.Role}
}

type RuleResponse struct {
	ID         uuid.UUID     `json:"id"`
	ProjectID  *uuid.UUID    `json:"project_id"`
	ApproverID uuid.UUID     `json:"approver_id"`
	Approver   ActorResponse `json:"approver"`
	Step       int16         `json:"step"`
}

func toRuleResponse(r approvalservice.Rule) RuleResponse {
	return RuleResponse{
		ID:         r.ID,
		ProjectID:  r.ProjectID,
		ApproverID: r.ApproverID,
		Approver:   toActorResponse(r.Approver),
		Step:       r.Step,
	}
}

func toRuleResponses(rs []approvalservice.Rule) []RuleResponse {
	out := make([]RuleResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, toRuleResponse(r))
	}
	return out
}

type SubmitResponse struct {
	TransactionID   uuid.UUID `json:"transaction_id"`
	Status          string    `json:"transaction_status"`
	FirstApproverID uuid.UUID `json:"first_approver_id"`
}

func toSubmitResponse(r approvalservice.SubmitResult) SubmitResponse {
	return SubmitResponse{
		TransactionID:   r.TransactionID,
		Status:          r.Status,
		FirstApproverID: r.FirstApproverID,
	}
}

type ApprovalStateResponse struct {
	ID          uuid.UUID     `json:"id"`
	Step        int16         `json:"step"`
	State       string        `json:"state"`
	Approver    ActorResponse `json:"approver"`
	ActivatedAt *time.Time    `json:"activated_at"`
}

func toApprovalStateResponse(a approvalservice.ApprovalStateView) ApprovalStateResponse {
	return ApprovalStateResponse{
		ID:          a.ID,
		Step:        a.Step,
		State:       a.State,
		Approver:    toActorResponse(a.Approver),
		ActivatedAt: a.ActivatedAt,
	}
}

type DecideResponse struct {
	Approval          ApprovalStateResponse `json:"approval"`
	TransactionStatus string                `json:"transaction_status"`
	NextStepActivated bool                  `json:"next_step_activated"`
}

func toDecideResponse(r approvalservice.DecideResult) DecideResponse {
	return DecideResponse{
		Approval:          toApprovalStateResponse(r.Approval),
		TransactionStatus: r.TransactionStatus,
		NextStepActivated: r.NextStepActivated,
	}
}

type AssignedResponse struct {
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
	CreatedBy     ActorResponse `json:"created_by"`
	DocumentCount int64         `json:"document_count"`
	SubmittedAt   time.Time     `json:"submitted_at"`
}

func toAssignedResponse(a approvalservice.AssignedItem) AssignedResponse {
	return AssignedResponse{
		ApprovalID:    a.ApprovalID,
		Step:          a.Step,
		ActivatedAt:   a.ActivatedAt,
		TransactionID: a.TransactionID,
		Status:        a.Status,
		TotalAmount:   a.TotalAmount,
		Currency:      a.Currency,
		Description:   a.Description,
		Category:      a.Category,
		ProjectID:     a.ProjectID,
		ProjectName:   a.ProjectName,
		CreatedBy:     toActorResponse(a.CreatedBy),
		DocumentCount: a.DocumentCount,
		SubmittedAt:   a.SubmittedAt,
	}
}

func toAssignedResponses(items []approvalservice.AssignedItem) []AssignedResponse {
	out := make([]AssignedResponse, 0, len(items))
	for _, a := range items {
		out = append(out, toAssignedResponse(a))
	}
	return out
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}
