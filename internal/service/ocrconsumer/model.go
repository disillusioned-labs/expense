package ocrconsumer

import "github.com/google/uuid"

// Event types expense accepts on its routed topic. ocr publishes
// document.processed; ocr-gateway re-publishes it as document.routed with the
// value untouched (source of truth: docs/reference/api-ocr.md and
// api-ocr-gateway.md). Anything else on the topic is a contract violation.
const (
	EventDocumentProcessed = "document.processed"
	EventDocumentRouted    = "document.routed"
)

// Field statuses from DocumentResult (FieldStatus enum). Tolerant Reader:
// an unknown value is treated as low_confidence - the safe path is needs_review,
// never a crash or a dead letter (source of truth: api-ocr.md, evolusi skema).
const (
	FieldStatusExtracted     = "extracted"
	FieldStatusLowConfidence = "low_confidence"
	FieldStatusMissing       = "missing"
	FieldStatusInvalid       = "invalid"
)

// ProcessedMessage is one decoded routed event, ready for Handle.
type ProcessedMessage struct {
	EventID     string
	DocumentID  string // id job di ocr
	ExternalRef uuid.UUID
	Status      string // "completed" | "needs_review" | "failed" - disimpulkan, bukan field envelope
	Result      *DocumentResult
	Error       *EventError
}

// EventError mirrors envelope.error (failed events only).
type EventError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DocumentResult mirrors the contract shape that flows inside data. It is
// stored verbatim into documents.ocr_data; the structs exist only so the
// consumer can find fields by name.
type DocumentResult struct {
	SchemaVersion string         `json:"schema_version"`
	AvgConfidence float64        `json:"avg_confidence"`
	Fields        []ExtractField `json:"fields"`
	Issues        []Issue        `json:"issues"`
}

type ExtractField struct {
	Name       string  `json:"name"`
	Value      string  `json:"value,omitempty"`
	Amount     int64   `json:"amount,omitempty"`
	Currency   string  `json:"currency,omitempty"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
	Page       int32   `json:"page,omitempty"`
}

type Issue struct {
	Code   string `json:"code"`
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// processedEnvelope is the wire shape of the record value - identical for
// document.processed and document.routed (the gateway forwards the value
// untouched). data and error are mutually exclusive and never both absent.
type processedEnvelope struct {
	SchemaVersion  string          `json:"schema_version"`
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	OccurredAt     string          `json:"occurred_at"`
	DocumentID     string          `json:"document_id"`
	ExternalRef    string          `json:"external_ref"`
	CallerID       string          `json:"caller_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Producer       string          `json:"producer"`
	Data           *DocumentResult `json:"data"`
	Error          *EventError     `json:"error"`
}

// OCRItem is one transaction_items row derived from the result. MVP mapping
// (source of truth: api-ocr.md, "Integrasi dengan rancangan expense"): one
// row from the `total` money field - line items of the nota itself are a
// deferred addition to the receipt schema.
type OCRItem struct {
	Description string
	Quantity    int32
	Amount      int64
}

// ocrData is the canonical shape persisted into documents.ocr_data: the
// DocumentResult copy, per contract - a copy, not the only source (ocr owns
// the original; sweep GetDocument can rebuild it).
type ocrData struct {
	DocumentResult *DocumentResult `json:"document_result"`
}
