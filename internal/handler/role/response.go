package role

import (
	"github.com/google/uuid"

	roleservice "github.com/disillusioned-labs/expense/internal/service/role"
)

type RoleResponse struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	IsSystem  bool      `json:"is_system"`
	Actions   []string  `json:"actions"`
	CreatedAt string    `json:"created_at"`
}

func toRoleResponse(r roleservice.Role) RoleResponse {
	return RoleResponse{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		Name:      r.Name,
		IsSystem:  r.IsSystem,
		Actions:   r.Actions,
		CreatedAt: r.CreatedAt,
	}
}

func toRoleResponses(rs []roleservice.Role) []RoleResponse {
	out := make([]RoleResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, toRoleResponse(r))
	}
	return out
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}
