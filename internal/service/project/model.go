package project

import (
	"time"

	"github.com/google/uuid"
)

type CreateInput struct {
	Name string
}

type UpdateInput struct {
	Name string
}

type PlaceMemberInput struct {
	UserID uuid.UUID
	RoleID uuid.UUID
}

type Project struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	Archived  bool      `json:"archived"`
}

type Member struct {
	UserID    uuid.UUID `json:"id"`
	RoleID    uuid.UUID `json:"role_id"`
	RoleName  string    `json:"role_name"`
	IsSystem  bool      `json:"role_is_system"`
	CreatedAt time.Time `json:"created_at"`
}
