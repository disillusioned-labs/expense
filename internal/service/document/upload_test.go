package document

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
)

func TestSameUploadRequest(t *testing.T) {
	transactionID := uuid.New()
	input := UploadInput{
		IdempotencyKey: uuid.New(),
		FileName:       "receipt.jpg",
		MimeType:       "image/jpeg",
		Size:           12,
		Content:        strings.NewReader("receipt-data"),
	}
	upload := repository.GetDocumentUploadByKeyRow{
		TransactionID: &transactionID,
		FileName:      input.FileName,
		MimeType:      input.MimeType,
		FileSize:      input.Size,
		FileHash:      pgtype.Text{String: "digest", Valid: true},
	}

	if !sameUploadRequest(upload, &transactionID, input, "digest") {
		t.Fatal("same request did not match its reservation")
	}
	if sameUploadRequest(upload, &transactionID, input, "different-digest") {
		t.Fatal("different file content matched its reservation")
	}
	otherTransaction := uuid.New()
	if sameUploadRequest(upload, &otherTransaction, input, "digest") {
		t.Fatal("different transaction matched its reservation")
	}
	if sameUploadRequest(upload, nil, input, "digest") {
		t.Fatal("project-level upload matched transaction upload")
	}
}

func TestSubmitOCRUsesConfiguredStorageBucketAndStableDocumentIdentity(t *testing.T) {
	documentID := uuid.New()
	var got ocr.SubmitInput
	service := &documentService{
		bucket: "expense",
		ocr: submitterFunc(func(_ context.Context, in ocr.SubmitInput) (string, error) {
			got = in
			return "", nil
		}),
		log: slog.New(slog.DiscardHandler),
	}
	service.submitOCR(context.Background(), documentID, "org/project/object.jpg", 12, "image/jpeg")
	if got.Bucket != "expense" || got.StoragePath != "org/project/object.jpg" {
		t.Fatalf("OCR source = bucket %q path %q; want configured bucket and persisted object key", got.Bucket, got.StoragePath)
	}
	if got.IdempotencyKey != documentID.String() || got.ExternalRef != documentID.String() {
		t.Fatalf("OCR identity = key %q ref %q; want document ID %q", got.IdempotencyKey, got.ExternalRef, documentID)
	}
}

type submitterFunc func(context.Context, ocr.SubmitInput) (string, error)

func (f submitterFunc) SubmitDocument(ctx context.Context, in ocr.SubmitInput) (string, error) {
	return f(ctx, in)
}
