package transaction

import "github.com/google/uuid"

const (
	EventTransactionCreated = "transaction.created"
	EventTransactionUpdated = "transaction.updated"
	EventTransactionDeleted = "transaction.deleted"
)

type TransactionCreatedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	TotalAmount    int64     `json:"total_amount"`
	ActorID        uuid.UUID `json:"actor_id"`
}

type TransactionUpdatedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	TotalAmount    int64     `json:"total_amount"`
	ActorID        uuid.UUID `json:"actor_id"`
	Source         string    `json:"source,omitempty"`
}

type TransactionDeletedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}

const (
	SourceManual = "manual"
)
