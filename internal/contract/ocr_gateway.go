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
// OcrGateway implements ocr.Submitter and ocr.StatusReader against the
// ocr-gateway's Kontrak A surface (ocr.gateway.v1.DocumentGateway). Submit is
// async: a nil error means "queued at the gateway", not "extracted" - results
// arrive via the routed Kafka topic, consumed by service/ocrconsumer.
type OcrGateway = grpcOcrGateway

type grpcOcrGateway struct {
	client gatewaypb.DocumentGatewayClient
	log    *slog.Logger
}

// NewGRPCOcrGateway builds the Kontrak A submitter over an established
// connection. The returned value also implements ocr.StatusReader; type it
// explicitly where both surfaces are needed.
func NewGRPCOcrGateway(conn *platformgrpc.Client, log *slog.Logger) ocr.Submitter {
	return NewOcrGateway(conn, log)
}

// NewOcrGateway returns the concrete gateway adapter implementing both
// ocr.Submitter and ocr.StatusReader over one connection.
func NewOcrGateway(conn *platformgrpc.Client, log *slog.Logger) *OcrGateway {
	return &OcrGateway{
		client: gatewaypb.NewDocumentGatewayClient(conn.Conn()),
		log:    log,
	}
}

func (c *grpcOcrGateway) SubmitDocument(ctx context.Context, in ocr.SubmitInput) (string, error) {
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
		return "", err
	}

	c.log.InfoContext(ctx, "document queued for OCR",
		"document_ref", in.ExternalRef, "gateway_document_id", resp.GetDocumentId())
	return resp.GetDocumentId(), nil
}

// GetDocumentStatus implements ocr.StatusReader against Kontrak A's
// GetDocument - the reconciliation sweep's safety net for routed events that
// never arrive.
func (c *grpcOcrGateway) GetDocumentStatus(ctx context.Context, documentID string) (ocr.DocumentStatusSnapshot, error) {
	ctx, span := ocrGatewayTracer.Start(ctx, "OcrGateway.GetDocumentStatus")
	defer span.End()

	resp, err := c.client.GetDocument(ctx, &gatewaypb.GetDocumentRequest{DocumentId: documentID})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gateway get document failed")
		return ocr.DocumentStatusSnapshot{}, err
	}

	snap := ocr.DocumentStatusSnapshot{
		Status: statusString(resp.GetStatus()),
	}

	if result := resp.GetResult(); result != nil {
		snap.AvgConfidence = result.GetAvgConfidence()
		for _, f := range result.GetFields() {
			snap.Fields = append(snap.Fields, ocr.StatusField{
				Name:       f.GetName(),
				Value:      f.GetValue(),
				Amount:     f.GetAmount(),
				Currency:   f.GetCurrency(),
				Confidence: f.GetConfidence(),
				Status:     fieldStatusString(f.GetStatus()),
			})
		}
	}

	if e := resp.GetError(); e != nil {
		snap.ErrorCode = e.GetCode()
		snap.ErrorDetail = e.GetMessage()
	}

	return snap, nil
}

func statusString(s gatewaypb.DocumentStatus) string {
	switch s {
	case gatewaypb.DocumentStatus_DOCUMENT_STATUS_QUEUED:
		return ocr.StatusQueued
	case gatewaypb.DocumentStatus_DOCUMENT_STATUS_PROCESSING:
		return ocr.StatusProcessing
	case gatewaypb.DocumentStatus_DOCUMENT_STATUS_COMPLETED:
		return ocr.StatusCompleted
	case gatewaypb.DocumentStatus_DOCUMENT_STATUS_NEEDS_REVIEW:
		return ocr.StatusNeedsReview
	case gatewaypb.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return ocr.StatusFailed
	default:
		return ""
	}
}

func fieldStatusString(s gatewaypb.FieldStatus) string {
	// Plain strings, matching the wire contract's field_status values that
	// service/ocrconsumer switches on - the contract package must not import
	// the consumer to share them.
	switch s {
	case gatewaypb.FieldStatus_FIELD_STATUS_EXTRACTED:
		return "extracted"
	case gatewaypb.FieldStatus_FIELD_STATUS_LOW_CONFIDENCE:
		return "low_confidence"
	case gatewaypb.FieldStatus_FIELD_STATUS_MISSING:
		return "missing"
	case gatewaypb.FieldStatus_FIELD_STATUS_INVALID:
		return "invalid"
	default:
		return ""
	}
}
