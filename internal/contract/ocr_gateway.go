package contract

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	gatewaypb "github.com/disillusioned-labs/ocr-gateway/contract/ocr/gateway/v1"
	platformgrpc "github.com/disillusioned-labs/platform/grpc"

	"github.com/disillusioned-labs/expense/internal/ocr"
)

var ocrGatewayTracer = otel.Tracer("contract/ocr_gateway")

// grpcOcrGateway implements internal/ocr.Submitter against the ocr-gateway's
// Kontrak A surface (ocr.gateway.v1.DocumentGateway). Submit is async: a nil
// return means "queued at the gateway", not "extracted" - results arrive via
// the routed Kafka topic, consumed by service/ocrconsumer.
type grpcOcrGateway struct {
	client gatewaypb.DocumentGatewayClient
	log    *slog.Logger
}

func NewGRPCOcrGateway(conn *platformgrpc.Client, log *slog.Logger) ocr.Submitter {
	return &grpcOcrGateway{
		client: gatewaypb.NewDocumentGatewayClient(conn.Conn()),
		log:    log,
	}
}

func (c *grpcOcrGateway) SubmitDocument(ctx context.Context, in ocr.SubmitInput) error {
	ctx, span := ocrGatewayTracer.Start(ctx, "OcrGateway.SubmitDocument")
	defer span.End()

	span.SetAttributes(
		attribute.String("gateway.doc_type", in.DocType),
		attribute.String("gateway.bucket", in.Bucket),
	)

	resp, err := c.client.SubmitDocument(ctx, &gatewaypb.SubmitDocumentRequest{
		IdempotencyKey: in.IdempotencyKey,
		ExternalRef:    in.ExternalRef,
		DocType:        in.DocType,
		Source: &gatewaypb.FileSource{
			Bucket:       in.Bucket,
			StoragePath:  in.StoragePath,
			SizeBytes:    in.SizeBytes,
			DeclaredMime: in.DeclaredMime,
		},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gateway submit failed")
		c.log.WarnContext(ctx, "ocr-gateway submit failed",
			"error", err, "document_ref", in.ExternalRef, "storage_path", in.StoragePath)
		return err
	}

	c.log.InfoContext(ctx, "document queued for OCR",
		"document_ref", in.ExternalRef, "gateway_document_id", resp.GetDocumentId())
	return nil
}
