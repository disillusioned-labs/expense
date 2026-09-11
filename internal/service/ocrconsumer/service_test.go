package ocrconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
)

// fakeQuerier implements only what the consumer calls; every other Querier
// method panics via the embedded interface, so a new query reaching the fake
// unimplemented fails loudly instead of silently doing nothing.
type fakeQuerier struct {
	repository.Querier

	claimRows  int64
	doc        repository.GetDocumentForProcessingRow
	completeID uuid.UUID
	complete   repository.CompleteDocumentOCRParams
	attached   repository.AttachDocumentToTransactionParams
	failedID   uuid.UUID
	created    repository.CreateTransactionParams
	inserted   repository.InsertTransactionItemsParams
	locked     int
	emitted    []repository.CreateOutboxEventParams
}

type fakeStore struct {
	*fakeQuerier
	inTx bool
}

func (f *fakeStore) ExecTx(ctx context.Context, fn func(repository.Querier) error) error {
	if f.inTx {
		return errors.New("nested transaction")
	}
	f.inTx = true
	defer func() { f.inTx = false }()
	return fn(f.fakeQuerier)
}

func (f *fakeQuerier) ClaimDocumentForOCR(ctx context.Context, id uuid.UUID) (int64, error) {
	return f.claimRows, nil
}

func (f *fakeQuerier) GetDocumentForProcessing(ctx context.Context, id uuid.UUID) (repository.GetDocumentForProcessingRow, error) {
	return f.doc, nil
}

func (f *fakeQuerier) CompleteDocumentOCR(ctx context.Context, arg repository.CompleteDocumentOCRParams) (int64, error) {
	f.completeID = arg.ID
	f.complete = arg
	return 1, nil
}

func (f *fakeQuerier) AttachDocumentToTransaction(ctx context.Context, arg repository.AttachDocumentToTransactionParams) (int64, error) {
	f.attached = arg
	return 1, nil
}

func (f *fakeQuerier) FailDocumentOCR(ctx context.Context, id uuid.UUID) (int64, error) {
	f.failedID = id
	return 1, nil
}

func (f *fakeQuerier) CreateTransaction(ctx context.Context, arg repository.CreateTransactionParams) (repository.Transaction, error) {
	f.created = arg
	return repository.Transaction{ID: uuid.Must(uuid.NewV7())}, nil
}

func (f *fakeQuerier) LockTransaction(ctx context.Context, id uuid.UUID) (repository.Transaction, error) {
	f.locked++
	return repository.Transaction{
		ID: id, OrganizationID: f.doc.OrganizationID, Status: constant.StatusDraft,
	}, nil
}

func (f *fakeQuerier) GetTransaction(ctx context.Context, arg repository.GetTransactionParams) (repository.Transaction, error) {
	return repository.Transaction{ID: arg.ID, OrganizationID: arg.OrganizationID, TotalAmount: 12765}, nil
}

func (f *fakeQuerier) InsertTransactionItems(ctx context.Context, arg repository.InsertTransactionItemsParams) (int64, error) {
	f.inserted = arg
	return int64(len(arg.Descriptions)), nil
}

func (f *fakeQuerier) CreateOutboxEvent(ctx context.Context, arg repository.CreateOutboxEventParams) (repository.OutboxEvent, error) {
	f.emitted = append(f.emitted, arg)
	return repository.OutboxEvent{ID: uuid.Must(uuid.NewV7())}, nil
}

func newTestService(q *fakeQuerier) Service {
	return NewService(&fakeStore{fakeQuerier: q}, slog.New(slog.DiscardHandler))
}

func pendingDoc(txID *uuid.UUID) repository.GetDocumentForProcessingRow {
	org := uuid.Must(uuid.NewV7())
	return repository.GetDocumentForProcessingRow{
		ID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7()),
		TransactionID: txID, OrganizationID: org, OcrStatus: constant.OCRStatusProcessing,
		FileName:   "nota.jpg",
		UploadedBy: org, UploadedByName: "unknown", UploadedByEmail: "unknown", UploadedByRole: "member",
	}
}

// receipt builds a DocumentResult the way the contract defines it: fields by
// name, money via amount+currency, statuses from the FieldStatus enum.
func receipt(totalStatus string) *DocumentResult {
	return &DocumentResult{
		SchemaVersion: "1.0", AvgConfidence: 0.97,
		Fields: []ExtractField{
			{Name: "merchant", Value: "TOKO MAJU JAYA", Confidence: 0.98, Status: FieldStatusExtracted, Page: 1},
			{Name: "date", Value: "2026-08-14", Confidence: 0.97, Status: FieldStatusExtracted, Page: 1},
			{Name: "total", Amount: 12765, Currency: "IDR", Confidence: 0.99, Status: totalStatus, Page: 1},
		},
	}
}

func envelope(data *DocumentResult, err *EventError) ProcessedMessage {
	msg, err2 := ParseProcessed(mustJSON(processedEnvelope{
		SchemaVersion: "1.0", EventID: uuid.Must(uuid.NewV7()).String(),
		EventType: EventDocumentRouted, DocumentID: uuid.Must(uuid.NewV7()).String(),
		ExternalRef: uuid.Must(uuid.NewV7()).String(), CallerID: "expense",
		IdempotencyKey: uuid.Must(uuid.NewV7()).String(), Producer: "ocr-gateway",
		Data: data, Error: err,
	}))
	if err2 != nil {
		panic(err2)
	}
	return msg
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestHandleDuplicateEventClaimsZeroRowsAndSkips(t *testing.T) {
	q := &fakeQuerier{claimRows: 0}
	msg := envelope(receipt(FieldStatusExtracted), nil)
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if q.completeID != (uuid.UUID{}) || q.failedID != (uuid.UUID{}) {
		t.Fatal("duplicate event must not touch the document")
	}
}

func TestHandleFailedCreatesNothing(t *testing.T) {
	q := &fakeQuerier{claimRows: 1, doc: pendingDoc(nil)}
	msg := envelope(nil, &EventError{Code: "FILE_CORRUPT", Message: "magic bytes mismatch"})
	msg.ExternalRef = q.doc.ID
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if q.failedID != q.doc.ID {
		t.Fatalf("document not failed: %s", q.failedID)
	}
	if q.created.ProjectID != (uuid.UUID{}) {
		t.Fatal("failed OCR must not create a transaction")
	}
}

func TestHandleCompletedAutoCreatesDraftWithUploaderSnapshot(t *testing.T) {
	q := &fakeQuerier{claimRows: 1, doc: pendingDoc(nil)}
	msg := envelope(receipt(FieldStatusExtracted), nil)
	msg.ExternalRef = q.doc.ID
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if q.created.CreatedBy != q.doc.UploadedBy || q.created.CreatedByRole != q.doc.UploadedByRole {
		t.Fatal("draft must be attributed to the first uploader snapshot")
	}
	if q.inserted.Descriptions[0] != "TOKO MAJU JAYA" || q.inserted.Amounts[0] != 12765 {
		t.Fatalf("total field not mapped to the line item: %+v", q.inserted)
	}
	if q.attached.TransactionID == nil {
		t.Fatal("document not linked to the new draft")
	}
	if len(q.emitted) != 1 || q.emitted[0].EventType != EventTransactionCreated {
		t.Fatalf("expected one transaction.created, got %d", len(q.emitted))
	}
	var stored ocrData
	if err := json.Unmarshal(q.attached.OcrData, &stored); err != nil {
		t.Fatalf("ocr_data is not the DocumentResult copy: %v", err)
	}
	if stored.DocumentResult.AvgConfidence == 0 {
		t.Fatal("ocr_data copy lost the result")
	}
}

func TestHandleLowConfidenceTotalParksForReviewWithoutDraft(t *testing.T) {
	q := &fakeQuerier{claimRows: 1, doc: pendingDoc(nil)}
	msg := envelope(receipt(FieldStatusLowConfidence), nil)
	msg.ExternalRef = q.doc.ID
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if msg.Status != StatusNeedsReview {
		t.Fatalf("expected derived status needs_review, got %q", msg.Status)
	}
	if q.complete.OcrStatus != constant.OCRStatusNeedsReview {
		t.Fatalf("expected needs_review, got %q", q.complete.OcrStatus)
	}
	if q.created.ProjectID != (uuid.UUID{}) {
		t.Fatal("unusable total must not create a draft")
	}
}

func TestHandleNonIDRTotalParksForReview(t *testing.T) {
	q := &fakeQuerier{claimRows: 1, doc: pendingDoc(nil)}
	r := receipt(FieldStatusExtracted)
	r.Fields[2].Currency = "USD"
	msg := envelope(r, nil)
	msg.ExternalRef = q.doc.ID
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if q.complete.OcrStatus != constant.OCRStatusNeedsReview {
		t.Fatalf("expected needs_review, got %q", q.complete.OcrStatus)
	}
}

func TestHandleAttachAppendsToExistingDraft(t *testing.T) {
	txID := uuid.Must(uuid.NewV7())
	q := &fakeQuerier{claimRows: 1, doc: pendingDoc(&txID)}
	msg := envelope(receipt(FieldStatusExtracted), nil)
	msg.ExternalRef = q.doc.ID
	if err := newTestService(q).Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if q.created.ProjectID != (uuid.UUID{}) {
		t.Fatal("attach path must not create another transaction")
	}
	if q.inserted.Amounts[0] != 12765 {
		t.Fatal("item not appended to existing draft")
	}
	if q.attached.TransactionID == nil || *q.attached.TransactionID != txID {
		t.Fatal("document not bound to the existing draft")
	}
	if len(q.emitted) != 1 || q.emitted[0].EventType != EventTransactionUpdated {
		t.Fatalf("expected one transaction.updated, got %d", len(q.emitted))
	}
}

func TestParseProcessedContractViolationsDeadLetter(t *testing.T) {
	docID := uuid.Must(uuid.NewV7()).String()
	cases := []struct {
		name  string
		value string
	}{
		{"bad json", `{`},
		{"missing external_ref", `{"event_id":"x","event_type":"document.routed","data":{}}`},
		{"bad external_ref", `{"event_id":"x","event_type":"document.routed","external_ref":"nope","data":{}}`},
		{"unknown event type", `{"event_id":"x","event_type":"something.else","external_ref":"` + docID + `","data":{}}`},
		{"both data and error", `{"event_id":"x","event_type":"document.routed","external_ref":"` + docID + `","data":{},"error":{"code":"X"}}`},
		{"neither data nor error", `{"event_id":"x","event_type":"document.routed","external_ref":"` + docID + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseProcessed([]byte(tc.value)); err == nil {
				t.Fatal("expected contract violation error")
			}
		})
	}
}

func TestParseProcessedTolerantReaderKeepsUnknownFieldStatus(t *testing.T) {
	docID := uuid.Must(uuid.NewV7())
	raw := mustJSON(processedEnvelope{
		SchemaVersion: "1.0", EventID: uuid.Must(uuid.NewV7()).String(),
		EventType: EventDocumentRouted, DocumentID: uuid.Must(uuid.NewV7()).String(),
		ExternalRef: docID.String(), CallerID: "expense", Producer: "ocr-gateway",
		Data: &DocumentResult{
			Fields: []ExtractField{
				{Name: "total", Amount: 5000, Currency: "IDR", Status: "brand_new_status"},
			},
		},
	})
	msg, err := ParseProcessed(raw)
	if err != nil {
		t.Fatalf("ParseProcessed: %v", err)
	}
	if msg.Status != StatusNeedsReview {
		t.Fatalf("unknown field status must degrade to needs_review, got %q", msg.Status)
	}
}
