package document

import "github.com/google/uuid"

const (
	eventVersion          = 1
	EventDocumentCreated  = "document.created"
	EventDocumentDeleted  = "document.deleted"
	EventDocumentAccessed = "document.accessed"
)

type DocumentCreatedEvent struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	DocumentID     uuid.UUID  `json:"document_id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	TransactionID  *uuid.UUID `json:"transaction_id,omitempty"`
	FileName       string     `json:"file_name"`
	FileSize       int64      `json:"file_size"`
	ActorID        uuid.UUID  `json:"actor_id"`
}

type DocumentDeletedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	DocumentID     uuid.UUID `json:"document_id"`
	FileName       string    `json:"file_name,omitempty"`
	ActorID        uuid.UUID `json:"actor_id"`
}

// DocumentAccessedEvent covers the sensitive-read case: fetching a single
// document builds a presigned URL that exposes the file content, so the
// disclosure itself is audited. List endpoints only render metadata and
// deliberately do not emit this event.
type DocumentAccessedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	DocumentID     uuid.UUID `json:"document_id"`
	ActorID        uuid.UUID `json:"actor_id"`
}
