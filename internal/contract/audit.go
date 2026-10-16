package contract

import "github.com/google/uuid"

// Audit event names emitted when a transaction enters or completes approval.
const (
	EventTransactionSubmitted = "transaction.submitted"
	EventTransactionDecided   = "transaction.decided"
)

// TransactionSubmittedEvent is the audit payload for submitting a transaction.
type TransactionSubmittedEvent struct {
	OrganizationID uuid.UUID  `json:"organization_id"`
	TransactionID  uuid.UUID  `json:"transaction_id"`
	ActorID        uuid.UUID  `json:"actor_id"`
	BatchID        *uuid.UUID `json:"batch_id,omitempty"`
}

// TransactionDecidedEvent is the audit payload for an approval decision.
type TransactionDecidedEvent struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	ApprovalID     uuid.UUID `json:"approval_id"`
	Decision       string    `json:"decision"`
	ActorID        uuid.UUID `json:"actor_id"`
}
