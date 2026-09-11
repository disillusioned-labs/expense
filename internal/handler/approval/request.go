package approval

import (
	"github.com/google/uuid"

	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
)

type CreateRuleRequest struct {
	ProjectID  *uuid.UUID `json:"project_id"`
	ApproverID uuid.UUID  `json:"approver_id" validate:"required"`
	Step       int16      `json:"step" validate:"min=1,max=10"`
}

type UpdateRuleRequest struct {
	Step int16 `json:"step" validate:"min=1,max=10"`
}

type DecideRequest struct {
	Decision string  `json:"decision" validate:"required,oneof=approved rejected"`
	Notes    *string `json:"notes"`
}

func (r CreateRuleRequest) ToInput() approvalservice.CreateRuleInput {
	return approvalservice.CreateRuleInput{ProjectID: r.ProjectID, ApproverID: r.ApproverID, Step: r.Step}
}

func (r UpdateRuleRequest) ToInput() approvalservice.UpdateRuleInput {
	return approvalservice.UpdateRuleInput{Step: r.Step}
}

func (r DecideRequest) ToInput() approvalservice.DecideInput {
	return approvalservice.DecideInput{Decision: r.Decision, Notes: r.Notes}
}
