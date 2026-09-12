package ocr

import (
	"context"
	"log/slog"
)

type Submitter interface {
	SubmitDocument(ctx context.Context, in SubmitInput) error
}

type SubmitInput struct {
	IdempotencyKey string // UUID dari expense - pelindung kirim-ulang
	ExternalRef    string // document_id expense - dibalas apa adanya di semua event
	DocType        string // "receipt" | ... - gateway yang memetakan ke schema_id
	Bucket         string // bucket pemohon - wajib terdaftar di allowlist config ocr
	StoragePath    string // path objek di bucket (bukan URL)
	SizeBytes      int64
	DeclaredMime   string
}

// noopSubmitter is the fallback when OCR_GATEWAY_TARGET is empty: submits log
// and return, documents stay pending OCR. The gRPC Kontrak A submitter lives
// in internal/contract (NewGRPCOcrGateway) and is selected in app/grpc_client.go.
type noopSubmitter struct{ log *slog.Logger }

func NewNoopSubmitter(log *slog.Logger) Submitter {
	return &noopSubmitter{log: log}
}

func (s *noopSubmitter) SubmitDocument(ctx context.Context, in SubmitInput) error {
	s.log.WarnContext(ctx,
		"OCR gateway not configured (OCR_GATEWAY_TARGET empty); document stays pending OCR",
		"document_ref", in.ExternalRef,
		"storage_path", in.StoragePath,
	)
	return nil
}
