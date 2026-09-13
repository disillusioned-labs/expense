package project

import "github.com/google/uuid"

const (
	EventProjectCreated       = "project.created"
	EventProjectUpdated       = "project.updated"
	EventProjectMemberPlaced  = "project_member.placed"
	EventProjectMemberRemoved = "project_member.removed"
	EventProjectArchived      = "project.archived"
	EventProjectUnarchived    = "project.unarchived"
)

type ProjectCreatedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type ProjectUpdatedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type ProjectMemberPlacedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	UserID         uuid.UUID `json:"user_id"`
	RoleID         uuid.UUID `json:"role_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type ProjectMemberRemovedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	UserID         uuid.UUID `json:"user_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type ProjectArchivedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}
