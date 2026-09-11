package role

import "github.com/google/uuid"

type CreateInput struct {
	Name    string
	Actions []string
}

type UpdateInput struct {
	Name    *string
	Actions *[]string
}

type Role struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	IsSystem  bool      `json:"is_system"`
	Actions   []string  `json:"actions"`
	CreatedAt string    `json:"created_at"`
}
