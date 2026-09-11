package project

import (
	"github.com/google/uuid"

	projectservice "github.com/disillusioned-labs/expense/internal/service/project"
)

type CreateRequest struct {
	Name string `json:"name" validate:"required,max=255"`
}

type UpdateRequest struct {
	Name string `json:"name" validate:"required,max=255"`
}

type PlaceMemberRequest struct {
	RoleID uuid.UUID `json:"role_id" validate:"required"`
}

func (r CreateRequest) ToInput() projectservice.CreateInput {
	return projectservice.CreateInput{Name: r.Name}
}

func (r UpdateRequest) ToInput() projectservice.UpdateInput {
	return projectservice.UpdateInput{Name: r.Name}
}

func (r PlaceMemberRequest) ToInput(userID uuid.UUID) projectservice.PlaceMemberInput {
	return projectservice.PlaceMemberInput{UserID: userID, RoleID: r.RoleID}
}
