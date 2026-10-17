// sweep.go is the OCR reconciliation safety net: it retries stale uploads
// missing a gateway job ID and asks for terminal status when routed events are
// lost. The claim keeps worker instances and the event path from duplicating
// document processing.
package ocrconsumer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
)

// Sweeper resolves documents whose routed event never arrived by asking the
// gateway directly.
type Sweeper struct {
	Service
	repo      repository.Store
	statuses  ocr.StatusReader
	submitter ocr.Submitter
	bucket    string
	identity  contract.IdentityClient
	log       *slog.Logger
}

// NewSweeper builds OCR recovery; the embedded Service applies terminal
// answers through its Handle path.
func NewSweeper(repo repository.Store, statuses ocr.StatusReader, submitter ocr.Submitter, bucket string, identity contract.IdentityClient, log *slog.Logger) *Sweeper {
	return &Sweeper{
		Service:   NewService(repo, identity, log),
		repo:      repo,
		statuses:  statuses,
		submitter: submitter,
		bucket:    bucket,
		identity:  identity,
		log:       log,
	}
}

// RetryUnsubmitted resubmits stale uploaded documents that never received a
// gateway job ID. Expense document IDs are stable gateway idempotency keys,
// so a lost submit response safely resolves to the existing job.
func (s *Sweeper) RetryUnsubmitted(ctx context.Context, olderThan, retryAfter time.Duration, limit int) (int, error) {
	now := time.Now()
	docs, err := s.repo.ClaimStaleOCRSubmissions(ctx, repository.ClaimStaleOCRSubmissionsParams{
		CreatedAt:            now.Add(-olderThan),
		OcrSubmitAttemptedAt: pgtype.Timestamptz{Time: now.Add(-retryAfter), Valid: true},
		Limit:                int32(limit),
	})
	if err != nil {
		return 0, err
	}

	retried := 0
	for _, doc := range docs {
		if ctx.Err() != nil {
			return retried, ctx.Err()
		}
		lease, err := s.identity.HoldExpenseWriteAccess(ctx, doc.OrganizationID, doc.UploadedBy)
		if err != nil {
			s.log.ErrorContext(ctx, "ocr resubmit authorization failed", "error", err, "document_id", doc.ID)
			continue
		}
		if lease.Context == nil || lease.Release == nil {
			lease.Close()
			s.log.ErrorContext(ctx, "ocr resubmit authorization returned an invalid lease", "document_id", doc.ID)
			continue
		}
		if lease.IsDeleted || lease.IsFrozen || !lease.MemberActive || !lease.ServiceAllowed {
			lease.Close()
			continue
		}

		ocrID, err := s.submitter.SubmitDocument(lease.Context, ocr.SubmitInput{
			IdempotencyKey: doc.ID.String(), ExternalRef: doc.ID.String(), DocType: "receipt",
			Bucket: s.bucket, StoragePath: doc.StoragePath, SizeBytes: doc.FileSize, DeclaredMime: doc.MimeType,
		})
		if err != nil {
			lease.Close()
			s.log.ErrorContext(ctx, "ocr resubmit failed", "error", err, "document_id", doc.ID)
			continue
		}
		if ocrID == "" {
			lease.Close()
			s.log.WarnContext(ctx, "ocr resubmit returned an empty job id", "document_id", doc.ID)
			continue
		}
		parsedID, err := uuid.Parse(ocrID)
		if err != nil {
			lease.Close()
			s.log.ErrorContext(ctx, "ocr resubmit returned an invalid job id", "document_id", doc.ID)
			continue
		}
		rows, err := s.repo.SetOcrDocumentID(lease.Context, repository.SetOcrDocumentIDParams{
			ID: doc.ID, OcrDocumentID: &parsedID,
		})
		lease.Close()
		if err != nil {
			s.log.ErrorContext(ctx, "saving recovered ocr job id failed", "error", err, "document_id", doc.ID)
			continue
		}
		if rows == 0 {
			continue
		}

		retried++
		s.log.InfoContext(ctx, "stale document submitted to ocr gateway", "document_id", doc.ID)
	}

	return retried, nil
}

// SweepStale asks the gateway once per document whose OCR has been pending
// longer than olderThan, and applies every terminal answer through Handle.
// A gateway or Handle failure for one document is logged and skipped - the
// next tick covers it - so one bad document cannot stall the sweep.
func (s *Sweeper) SweepStale(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	docs, err := s.repo.ListStaleOCRDocuments(ctx, repository.ListStaleOCRDocumentsParams{
		CreatedAt: time.Now().Add(-olderThan),
		Limit:     int32(limit),
	})
	if err != nil {
		return 0, err
	}

	swept := 0
	for _, doc := range docs {
		snap, err := s.statuses.GetDocumentStatus(ctx, doc.OcrDocumentID.String())
		if err != nil {
			s.log.ErrorContext(ctx, "ocr status lookup failed",
				"error", err, "document_id", doc.ID, "ocr_document_id", doc.OcrDocumentID)
			continue
		}

		// Non-terminal states are the gateway saying "still working"; the
		// routed event or a later sweep will deliver the answer.
		switch snap.Status {
		case ocr.StatusCompleted, ocr.StatusNeedsReview, ocr.StatusFailed:
		default:
			continue
		}

		if err := s.Handle(ctx, messageFromSnapshot(doc.ID, snap)); err != nil {
			if errors.Is(err, ErrWriteAccessDenied) {
				continue
			}
			s.log.ErrorContext(ctx, "sweep apply failed",
				"error", err, "document_id", doc.ID, "status", snap.Status)
			continue
		}

		swept++
		s.log.InfoContext(ctx, "stale ocr document resolved via sweep",
			"document_id", doc.ID, "status", snap.Status)
	}

	return swept, nil
}

// messageFromSnapshot maps a gateway answer onto the same ProcessedMessage
// the routed-event path produces, so Handle cannot tell the two apart.
func messageFromSnapshot(documentID uuid.UUID, snap ocr.DocumentStatusSnapshot) ProcessedMessage {
	if snap.Status == ocr.StatusFailed {
		return ProcessedMessage{
			// The event id only feeds logs; a sweep-derived prefix marks the
			// origin of the outcome.
			EventID:     "sweep-" + documentID.String(),
			DocumentID:  documentID.String(),
			ExternalRef: documentID,
			Status:      StatusFailed,
			Error:       &EventError{Code: snap.ErrorCode, Message: snap.ErrorDetail},
		}
	}

	result := &DocumentResult{
		AvgConfidence: snap.AvgConfidence,
	}
	for _, f := range snap.Fields {
		result.Fields = append(result.Fields, ExtractField{
			Name:       f.Name,
			Value:      f.Value,
			Amount:     f.Amount,
			Currency:   f.Currency,
			Confidence: f.Confidence,
			Status:     f.Status,
		})
	}

	return ProcessedMessage{
		EventID:     "sweep-" + documentID.String(),
		DocumentID:  documentID.String(),
		ExternalRef: documentID,
		Status:      snap.Status,
		Result:      result,
	}
}
