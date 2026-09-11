package project

import (
	"time"

	"github.com/google/uuid"

	projectservice "github.com/disillusioned-labs/expense/internal/service/project"
)

type ProjectResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	Archived  bool      `json:"archived"`
}

func toProjectResponse(p projectservice.Project) ProjectResponse {
	return ProjectResponse{
		ID:        p.ID,
		Name:      p.Name,
		CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt,
		Archived:  p.Archived,
	}
}

func toProjectResponses(ps []projectservice.Project) []ProjectResponse {
	out := make([]ProjectResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProjectResponse(p))
	}
	return out
}

type MemberResponse struct {
	ID           uuid.UUID `json:"id"`
	RoleID       uuid.UUID `json:"role_id"`
	RoleName     string    `json:"role_name"`
	RoleIsSystem bool      `json:"role_is_system"`
	CreatedAt    time.Time `json:"created_at"`
}

func toMemberResponse(m projectservice.Member) MemberResponse {
	return MemberResponse{
		ID:           m.UserID,
		RoleID:       m.RoleID,
		RoleName:     m.RoleName,
		RoleIsSystem: m.IsSystem,
		CreatedAt:    m.CreatedAt,
	}
}

func toMemberResponses(ms []projectservice.Member) []MemberResponse {
	out := make([]MemberResponse, 0, len(ms))
	for _, m := range ms {
		out = append(out, toMemberResponse(m))
	}
	return out
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}

type RemoveMemberResponse struct {
	Removed bool `json:"removed"`
}
