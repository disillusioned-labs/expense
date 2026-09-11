// Package ocrconsumer turns routed OCR events into draft transactions. It
// owns the transaction it creates: the claim on the document, the draft
// insert and the item rows all share one ExecTx, so a redelivered event can
// never produce a second draft. The wire contract it implements is the source
// of truth in docs/reference/api-ocr.md (envelope + DocumentResult) and
// api-ocr-gateway.md (routed topic).
package ocrconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	"github.com/disillusioned-labs/platform/pgutil"
)

var tracer = otel.Tracer("service/ocrconsumer")

const (
	EventTransactionCreated = "transaction.created"
	EventTransactionUpdated = "transaction.updated"

	SourceOCR = "ocr"

	StatusCompleted   = "completed"
	StatusNeedsReview = "needs_review"
	StatusFailed      = "failed"
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

type Service interface {
	Handle(ctx context.Context, msg ProcessedMessage) error
}

type ocrConsumerService struct {
	repo repository.Store
	log  *slog.Logger
}

func NewService(repo repository.Store, log *slog.Logger) Service {
	return &ocrConsumerService{repo: repo, log: log}
}

// ParseProcessed decodes a raw routed record value. A decode error means the
// payload does not honour the contract at all, so the caller dead-letters the
// record instead of retrying it.
func ParseProcessed(value []byte) (ProcessedMessage, error) {
	var env processedEnvelope
	if err := json.Unmarshal(value, &env); err != nil {
		return ProcessedMessage{}, fmt.Errorf("decode document event: %w", err)
	}
	if env.EventID == "" || env.DocumentID == "" {
		return ProcessedMessage{}, errors.New("document event missing event_id or document_id")
	}
	// external_ref is the expense document id (echoed from submit).
	docID, err := uuid.Parse(env.ExternalRef)
	if err != nil {
		return ProcessedMessage{}, fmt.Errorf("document event external_ref is not a document id: %w", err)
	}
	if env.EventType != EventDocumentProcessed && env.EventType != EventDocumentRouted {
		return ProcessedMessage{}, fmt.Errorf("document event type %q is not supported", env.EventType)
	}
	// data and error are mutually exclusive and never both absent - an event
	// breaking that invariant is undecidable, so it dead-letters.
	switch {
	case env.Data != nil && env.Error != nil:
		return ProcessedMessage{}, errors.New("document event carries both data and error")
	case env.Data == nil && env.Error == nil:
		return ProcessedMessage{}, errors.New("document event carries neither data nor error")
	}

	msg := ProcessedMessage{
		EventID:     env.EventID,
		DocumentID:  env.DocumentID,
		ExternalRef: docID,
		Result:      env.Data,
		Error:       env.Error,
	}
	switch {
	case env.Error != nil:
		msg.Status = StatusFailed
	default:
		// Production logic: classify based on critical field + avg confidence.
		// Total not extracted → failed; total extracted + avg >= 0.7 → completed;
		// total extracted + avg < 0.7 → needs_review.
		msg.Status = classifyResult(env.Data)
	}
	return msg, nil
}

func (s *ocrConsumerService) Handle(ctx context.Context, msg ProcessedMessage) error {
	ctx, span := tracer.Start(ctx, "OcrConsumer.Handle")
	defer span.End()

	// Claim first: only one delivery may drive the document out of
	// 'pending', so redeliveries and stale events claim zero rows and exit.
	// This database-level claim is the consumer's dedupe - at-least-once
	// delivery from Kafka never turns into double processing.
	claimed, err := s.repo.ClaimDocumentForOCR(ctx, msg.ExternalRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "claim document failed")
		s.log.ErrorContext(ctx, "claim document failed", "error", err, "document_id", msg.ExternalRef)
		return err
	}
	if claimed == 0 {
		s.log.InfoContext(ctx, "document not claimable; skipping",
			"document_id", msg.ExternalRef, "event_id", msg.EventID, "status", msg.Status)
		return nil
	}

	doc, err := s.repo.GetDocumentForProcessing(ctx, msg.ExternalRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load document failed")
		s.log.ErrorContext(ctx, "load document failed", "error", err, "document_id", msg.ExternalRef)
		return err
	}

	if msg.Status == StatusFailed {
		if _, err := s.repo.FailDocumentOCR(ctx, doc.ID); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "fail document failed")
			s.log.ErrorContext(ctx, "fail document failed", "error", err, "document_id", doc.ID)
			return err
		}
		s.log.InfoContext(ctx, "ocr failed; document awaits re-upload",
			"document_id", doc.ID, "error_code", msg.Error.Code)
		return nil
	}

	// ocr_data is the verbatim DocumentResult copy - a copy, not the source.
	data, err := json.Marshal(ocrData{DocumentResult: msg.Result})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal ocr data failed")
		return err
	}

	var item *OCRItem
	if msg.Status == StatusCompleted {
		item = totalItem(doc, msg.Result)
	}

	if doc.TransactionID != nil {
		return s.attach(ctx, doc, *doc.TransactionID, item, msg.Status, data)
	}
	return s.createDraft(ctx, doc, item, msg.Status, data)
}

// attach appends the OCR line item to a draft the document was already bound
// to at upload time. A transaction that left draft status meanwhile - or a
// result without a usable total - keeps the parse result for review instead
// of silently mutating the transaction.
func (s *ocrConsumerService) attach(ctx context.Context, doc repository.GetDocumentForProcessingRow, transactionID uuid.UUID, item *OCRItem, status string, data []byte) error {
	ctx, span := tracer.Start(ctx, "OcrConsumer.attach")
	defer span.End()

	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		locked, err := q.LockTransaction(ctx, transactionID)
		if err != nil {
			return err
		}
		if locked.DeletedAt.Valid {
			return service.ErrNotFound
		}
		if locked.Status != constant.StatusDraft || item == nil {
			_, err := q.CompleteDocumentOCR(ctx, repository.CompleteDocumentOCRParams{
				ID: doc.ID, OcrStatus: constant.OCRStatusNeedsReview, OcrData: data,
			})
			return err
		}
		if err := insertItems(ctx, q, transactionID, []OCRItem{*item}); err != nil {
			return err
		}
		if _, err := q.AttachDocumentToTransaction(ctx, repository.AttachDocumentToTransactionParams{
			ID: doc.ID, TransactionID: &transactionID,
			OcrStatus: constant.OCRStatusCompleted, OcrData: data,
		}); err != nil {
			return err
		}
		// The trigger keeps total_amount in sync; read it back for the event.
		refreshed, err := q.GetTransaction(ctx, repository.GetTransactionParams{
			ID: transactionID, OrganizationID: locked.OrganizationID,
		})
		if err != nil {
			return err
		}
		return service.Emit(ctx, q, "transaction", transactionID, EventTransactionUpdated, constant.TopicAudit, TransactionUpdatedEvent{
			OrganizationID: locked.OrganizationID, TransactionID: transactionID,
			TotalAmount: refreshed.TotalAmount, ActorID: doc.UploadedBy, Source: SourceOCR,
		})
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "attach ocr items failed")
		s.log.ErrorContext(ctx, "attach ocr items failed", "error", err, "document_id", doc.ID)
		return err
	}
	if status == StatusCompleted {
		s.log.InfoContext(ctx, "ocr item attached to draft", "document_id", doc.ID, "transaction_id", transactionID)
	} else {
		s.log.InfoContext(ctx, "ocr result parked for review", "document_id", doc.ID, "transaction_id", transactionID)
	}
	return nil
}

// createDraft auto-creates the transaction the OCR result describes. The
// draft is attributed to the first uploader via the snapshot frozen on the
// document row, because the consumer runs without an actor.
func (s *ocrConsumerService) createDraft(ctx context.Context, doc repository.GetDocumentForProcessingRow, item *OCRItem, status string, data []byte) error {
	ctx, span := tracer.Start(ctx, "OcrConsumer.createDraft")
	defer span.End()

	if item == nil {
		// needs_review: OCR ran but produced no trustworthy total - park the
		// result for review. No transaction exists yet, matching "no draft
		// until OCR succeeds" - the user re-uploads or reviews.
		if _, err := s.repo.CompleteDocumentOCR(ctx, repository.CompleteDocumentOCRParams{
			ID: doc.ID, OcrStatus: constant.OCRStatusNeedsReview, OcrData: data,
		}); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "complete document failed")
			s.log.ErrorContext(ctx, "complete document failed", "error", err, "document_id", doc.ID)
			return err
		}
		s.log.InfoContext(ctx, "ocr result needs review; no draft created", "document_id", doc.ID)
		return nil
	}

	var created repository.Transaction
	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		row, err := q.CreateTransaction(ctx, repository.CreateTransactionParams{
			OrganizationID: doc.OrganizationID,
			ProjectID:      doc.ProjectID,
			Description:    pgutil.Text(nil),
			Currency:       constant.CurrencyIDR,
			Category:       pgutil.Text(nil),
			CreatedBy:      doc.UploadedBy,
			CreatedByName:  doc.UploadedByName,
			CreatedByEmail: doc.UploadedByEmail,
			CreatedByRole:  doc.UploadedByRole,
		})
		if err != nil {
			return err
		}
		if err := insertItems(ctx, q, row.ID, []OCRItem{*item}); err != nil {
			return err
		}
		if _, err := q.AttachDocumentToTransaction(ctx, repository.AttachDocumentToTransactionParams{
			ID: doc.ID, TransactionID: &row.ID,
			OcrStatus: constant.OCRStatusCompleted, OcrData: data,
		}); err != nil {
			return err
		}
		// The trigger keeps total_amount in sync; read it back for the event.
		created, err = q.GetTransaction(ctx, repository.GetTransactionParams{
			ID: row.ID, OrganizationID: doc.OrganizationID,
		})
		if err != nil {
			return err
		}
		return service.Emit(ctx, q, "transaction", created.ID, EventTransactionCreated, constant.TopicAudit, TransactionCreatedEvent{
			OrganizationID: doc.OrganizationID, TransactionID: created.ID,
			ProjectID: doc.ProjectID, TotalAmount: created.TotalAmount, ActorID: doc.UploadedBy,
		})
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create draft failed")
		s.log.ErrorContext(ctx, "create draft failed", "error", err, "document_id", doc.ID)
		return err
	}
	s.log.InfoContext(ctx, "draft transaction created from ocr",
		"document_id", doc.ID, "transaction_id", created.ID, "total_amount", created.TotalAmount)
	return nil
}

// classifyResult determines the document status based on critical field
// extraction and average confidence. Production logic:
//   - Total not extracted → needs_review (OCR ran but no trustworthy total)
//   - Total extracted + avg >= 0.7 → completed (auto-create draft)
//   - Total extracted + avg < 0.7 → needs_review (create draft, flag for review)
func classifyResult(r *DocumentResult) string {
	if r == nil {
		return StatusNeedsReview
	}

	total := moneyField(r)
	if total == nil {
		return StatusNeedsReview
	}

	if r.AvgConfidence >= 0.7 {
		return StatusCompleted
	}
	return StatusNeedsReview
}

// totalItem maps the contract's `total` money field to the one transaction
// item the MVP creates (line items of the nota itself are a deferred receipt
// schema addition). Description falls back to the merchant name, then the
// file name. Any unusable total returns nil -> needs_review, no draft.
func totalItem(doc repository.GetDocumentForProcessingRow, r *DocumentResult) *OCRItem {
	total := moneyField(r)
	if total == nil {
		return nil
	}
	if total.Currency != constant.CurrencyIDR {
		return nil
	}
	if total.Amount < 0 {
		return nil
	}
	description := doc.FileName
	if merchant := findField(r, "merchant"); merchant != nil && merchant.Value != "" {
		description = merchant.Value
	}
	return &OCRItem{Description: description, Quantity: 1, Amount: total.Amount}
}

// moneyField returns the total field only when it is a clean extraction.
func moneyField(r *DocumentResult) *ExtractField {
	f := findField(r, "total")
	if f == nil {
		return nil
	}
	if f.Status != FieldStatusExtracted {
		return nil
	}
	return f
}

func findField(r *DocumentResult, name string) *ExtractField {
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			return &r.Fields[i]
		}
	}
	return nil
}

func insertItems(ctx context.Context, q repository.Querier, txID uuid.UUID, items []OCRItem) error {
	descriptions := make([]string, len(items))
	quantities := make([]int32, len(items))
	amounts := make([]int64, len(items))
	for i, it := range items {
		descriptions[i] = it.Description
		quantities[i] = it.Quantity
		amounts[i] = it.Amount
	}
	_, err := q.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
		TransactionID: txID,
		Descriptions:  descriptions,
		Quantities:    quantities,
		Amounts:       amounts,
	})
	return err
}
