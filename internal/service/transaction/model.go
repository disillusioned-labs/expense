package transaction

import (
	"time"

	"github.com/google/uuid"
)

type ActorBrief struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

type Transaction struct {
	ID            uuid.UUID  `json:"id"`
	Status        string     `json:"status"`
	TotalAmount   int64      `json:"total_amount"`
	Currency      string     `json:"currency"`
	Category      *string    `json:"category"`
	Description   *string    `json:"description"`
	Project       ProjectRef `json:"project"`
	CreatedBy     ActorBrief `json:"created_by"`
	DocumentCount int64      `json:"document_count"`
	CreatedAt     time.Time  `json:"created_at"`
	SubmittedAt   *time.Time `json:"submitted_at"`
	DecidedAt     *time.Time `json:"decided_at"`
}

type ProjectRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type DocumentBrief struct {
	ID        uuid.UUID `json:"id"`
	FileName  string    `json:"file_name"`
	FileURL   string    `json:"file_url"`
	MimeType  string    `json:"mime_type"`
	OcrStatus string    `json:"ocr_status"`
	CreatedAt time.Time `json:"created_at"`
}

type Decision struct {
	Decision  string     `json:"decision"`
	Notes     *string    `json:"notes"`
	DecidedBy ActorBrief `json:"decided_by"`
	DecidedAt time.Time  `json:"decided_at"`
}

type ApprovalState struct {
	ID          uuid.UUID  `json:"id"`
	Step        int16      `json:"step"`
	State       string     `json:"state"`
	Approver    ActorBrief `json:"approver"`
	ActivatedAt *time.Time `json:"activated_at"`
	Decision    *Decision  `json:"decision"`
}

type Item struct {
	ID          uuid.UUID `json:"id"`
	Description string    `json:"description"`
	Quantity    int32     `json:"quantity"`
	Amount      int64     `json:"amount"`
}

type ItemInput struct {
	Description string
	Quantity    int32
	Amount      int64
}

type Detail struct {
	Transaction
	Items     []Item          `json:"items"`
	Documents []DocumentBrief `json:"documents"`
	Approvals []ApprovalState `json:"approvals"`
}

type ListFilters struct {
	Status    string
	ProjectID *uuid.UUID
	CreatedBy *uuid.UUID
	Category  *string
	DateFrom  *time.Time
	DateTo    *time.Time
}

type UpdateInput struct {
	Description *string
	Items       []ItemInput
	Currency    string
	Category    *string
}
