package role

import "github.com/google/uuid"

const (
	EventRoleCreated = "role.created"
	EventRoleUpdated = "role.updated"
	EventRoleDeleted = "role.deleted"
)

type RoleCreatedEvent struct {
	ProjectID uuid.UUID `json:"project_id"`
	RoleID    uuid.UUID `json:"role_id"`
	Name      string    `json:"name"`
	Actions   []string  `json:"actions"`
	ActorID   uuid.UUID `json:"actor_id"`
}

type RoleUpdatedEvent struct {
	ProjectID uuid.UUID `json:"project_id"`
	RoleID    uuid.UUID `json:"role_id"`
	ActorID   uuid.UUID `json:"actor_id"`
}

type RoleDeletedEvent struct {
	ProjectID uuid.UUID `json:"project_id"`
	RoleID    uuid.UUID `json:"role_id"`
	ActorID   uuid.UUID `json:"actor_id"`
}
