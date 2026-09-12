package ocr

import "context"

// DocumentStatus values mirror the gateway's DocumentStatus enum; only the
// three terminal ones drive consumer behaviour, queued/processing mean
// "nothing to do yet".
const (
	StatusQueued      = "queued"
	StatusProcessing  = "processing"
	StatusCompleted   = "completed"
	StatusNeedsReview = "needs_review"
	StatusFailed      = "failed"
)

// StatusField is one extracted field of a status snapshot, carrying only what
// a consumer needs to apply a result.
type StatusField struct {
	Name       string
	Value      string
	Amount     int64
	Currency   string
	Confidence float64
	Status     string
}

// DocumentStatusSnapshot is the neutral view of a GetDocument answer. It
// exists so consumers depend on this package, not on the gateway proto.
type DocumentStatusSnapshot struct {
	Status        string
	AvgConfidence float64
	Fields        []StatusField
	ErrorCode     string // failed only
	ErrorDetail   string // failed only, for logs - never parsed
}

// StatusReader asks the ocr-gateway for the current state of a submitted job
// (Kontrak A GetDocument). It is the reconciliation sweep's safety net for
// routed events that never arrive.
type StatusReader interface {
	GetDocumentStatus(ctx context.Context, documentID string) (DocumentStatusSnapshot, error)
}
