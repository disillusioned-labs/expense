package transaction

import (
	"time"

	"github.com/google/uuid"

	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
)

type ActorBriefResponse struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

func toActorBriefResponse(a transactionservice.ActorBrief) ActorBriefResponse {
	return ActorBriefResponse{ID: a.ID, Name: a.Name, Email: a.Email, Role: a.Role}
}

type ProjectRefResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type ItemResponse struct {
	ID          uuid.UUID `json:"id"`
	Description string    `json:"description"`
	Quantity    int32     `json:"quantity"`
	Amount      int64     `json:"amount"`
}

type TransactionResponse struct {
	ID            uuid.UUID          `json:"id"`
	Status        string             `json:"status"`
	TotalAmount   int64              `json:"total_amount"`
	Currency      string             `json:"currency"`
	Category      *string            `json:"category"`
	Description   *string            `json:"description"`
	Items         []ItemResponse     `json:"items"`
	Project       ProjectRefResponse `json:"project"`
	CreatedBy     ActorBriefResponse `json:"created_by"`
	DocumentCount int64              `json:"document_count"`
	CreatedAt     time.Time          `json:"created_at"`
	SubmittedAt   *time.Time         `json:"submitted_at"`
	DecidedAt     *time.Time         `json:"decided_at"`
}

func toItemResponses(items []transactionservice.Item) []ItemResponse {
	out := make([]ItemResponse, 0, len(items))
	for _, it := range items {
		out = append(out, ItemResponse{ID: it.ID, Description: it.Description, Quantity: it.Quantity, Amount: it.Amount})
	}
	return out
}

func toTransactionResponse(t transactionservice.Transaction, items []transactionservice.Item) TransactionResponse {
	return TransactionResponse{
		ID:            t.ID,
		Status:        t.Status,
		TotalAmount:   t.TotalAmount,
		Currency:      t.Currency,
		Category:      t.Category,
		Description:   t.Description,
		Items:         toItemResponses(items),
		Project:       ProjectRefResponse{ID: t.Project.ID, Name: t.Project.Name},
		CreatedBy:     toActorBriefResponse(t.CreatedBy),
		DocumentCount: t.DocumentCount,
		CreatedAt:     t.CreatedAt,
		SubmittedAt:   t.SubmittedAt,
		DecidedAt:     t.DecidedAt,
	}
}

func toTransactionResponses(ts []transactionservice.Transaction) []TransactionResponse {
	out := make([]TransactionResponse, 0, len(ts))
	for _, t := range ts {
		out = append(out, toTransactionResponse(t, nil))
	}
	return out
}

type DocumentBriefResponse struct {
	ID        uuid.UUID `json:"id"`
	FileName  string    `json:"file_name"`
	FileURL   string    `json:"file_url"`
	MimeType  string    `json:"mime_type"`
	OcrStatus string    `json:"ocr_status"`
	CreatedAt time.Time `json:"created_at"`
}

type DecisionResponse struct {
	Decision  string             `json:"decision"`
	Notes     *string            `json:"notes"`
	DecidedBy ActorBriefResponse `json:"decided_by"`
	DecidedAt time.Time          `json:"decided_at"`
}

type ApprovalStateResponse struct {
	ID          uuid.UUID          `json:"id"`
	Step        int16              `json:"step"`
	State       string             `json:"state"`
	Approver    ActorBriefResponse `json:"approver"`
	ActivatedAt *time.Time         `json:"activated_at"`
	Decision    *DecisionResponse  `json:"decision"`
}

type DetailResponse struct {
	TransactionResponse
	Documents []DocumentBriefResponse `json:"documents"`
	Approvals []ApprovalStateResponse `json:"approvals"`
}

func ToDetailResponse(d transactionservice.Detail) DetailResponse {
	out := DetailResponse{
		TransactionResponse: toTransactionResponse(d.Transaction, d.Items),
		Documents:           make([]DocumentBriefResponse, 0, len(d.Documents)),
		Approvals:           make([]ApprovalStateResponse, 0, len(d.Approvals)),
	}
	for _, doc := range d.Documents {
		out.Documents = append(out.Documents, DocumentBriefResponse{
			ID:        doc.ID,
			FileName:  doc.FileName,
			FileURL:   doc.FileURL,
			MimeType:  doc.MimeType,
			OcrStatus: doc.OcrStatus,
			CreatedAt: doc.CreatedAt,
		})
	}
	for _, a := range d.Approvals {
		state := ApprovalStateResponse{
			ID:          a.ID,
			Step:        a.Step,
			State:       a.State,
			Approver:    toActorBriefResponse(a.Approver),
			ActivatedAt: a.ActivatedAt,
		}
		if a.Decision != nil {
			state.Decision = &DecisionResponse{
				Decision:  a.Decision.Decision,
				Notes:     a.Decision.Notes,
				DecidedBy: toActorBriefResponse(a.Decision.DecidedBy),
				DecidedAt: a.Decision.DecidedAt,
			}
		}
		out.Approvals = append(out.Approvals, state)
	}
	return out
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}
