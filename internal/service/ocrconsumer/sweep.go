// sweep.go is the reconciliation safety net: routed Kafka events are the
// primary OCR result delivery, but a lost event (or a consumer down past the
// retention window) would leave a document pending forever. The sweep asks
// the gateway about stale documents via Kontrak A GetDocument and feeds
// terminal answers through the same Handle path as events - the claim keeps
// the two delivery paths from double-processing one document.
package ocrconsumer

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
)

// Sweeper resolves documents whose routed event never arrived by asking the
// gateway directly.
type Sweeper struct {
	Service
	repo     repository.Store
	statuses ocr.StatusReader
	log      *slog.Logger
}

// NewSweeper builds the sweep; statuses is the gateway-backed StatusReader
// and the embedded Service applies terminal answers through its Handle path.
func NewSweeper(repo repository.Store, statuses ocr.StatusReader, log *slog.Logger) *Sweeper {
	return &Sweeper{
		Service:  NewService(repo, log),
		repo:     repo,
		statuses: statuses,
		log:      log,
	}
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
