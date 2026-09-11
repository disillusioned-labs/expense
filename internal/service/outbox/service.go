package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/platform/kafka"
)

var tracer = otel.Tracer("service/outbox")

const (
	defaultBatchSize  = 100
	defaultRetryDelay = 5 * time.Second
	sourceService     = "expense"
)

type OutboxService interface {
	PublishPending(ctx context.Context, workerID string, batchSize int) error
}

type outboxService struct {
	repo     repository.Store
	producer kafka.Producer
	log      *slog.Logger
}

func NewOutboxService(repo repository.Store, producer kafka.Producer, log *slog.Logger) OutboxService {
	return &outboxService{repo: repo, producer: producer, log: log}
}

func (s *outboxService) PublishPending(ctx context.Context, workerID string, batchSize int) error {
	ctx, span := tracer.Start(ctx, "OutboxService.PublishPending")
	defer span.End()

	span.SetAttributes(attribute.String("outbox.worker_id", workerID), attribute.Int("outbox.batch_size", batchSize))
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	events, err := s.repo.ClaimPendingOutboxEvents(ctx, repository.ClaimPendingOutboxEventsParams{
		Limit:    int32(batchSize),
		LockedBy: pgtype.Text{String: workerID, Valid: true},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "claim pending outbox events")
		s.log.ErrorContext(ctx, "claim pending outbox events failed", "error", err, "worker_id", workerID)
		return fmt.Errorf("claim pending outbox events: %w", err)
	}

	span.SetAttributes(attribute.Int("outbox.events_claimed", len(events)))
	for _, event := range events {
		_ = s.publishEvent(ctx, event)
	}
	return nil
}

func (s *outboxService) publishEvent(ctx context.Context, event repository.OutboxEvent) error {
	ctx, span := tracer.Start(ctx, "OutboxService.publishEvent")
	defer span.End()

	span.SetAttributes(
		attribute.String("outbox.event_id", event.ID.String()),
		attribute.String("outbox.event_type", event.EventType),
		attribute.String("outbox.aggregate_type", event.AggregateType),
		attribute.String("outbox.aggregate_id", event.AggregateID.String()),
		attribute.Int("outbox.attempt_count", int(event.AttemptCount)),
	)
	headers := make([]kafka.RecordHeader, 0, 8)
	carrier := kafka.NewHeaderCarrier(&headers)
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	headers = append(headers,
		kafka.NewRecordHeader("event-id", event.ID.String()),
		kafka.NewRecordHeader("event-type", event.EventType),
		kafka.NewRecordHeader("event-version", strconv.Itoa(int(event.EventVersion))),
		kafka.NewRecordHeader("source-service", sourceService),
		kafka.NewRecordHeader("aggregate-type", event.AggregateType),
		kafka.NewRecordHeader("aggregate-id", event.AggregateID.String()),
	)
	if event.TraceID.Valid {
		headers = append(headers, kafka.NewRecordHeader("trace-id", event.TraceID.String))
	}

	err := s.producer.Publish(ctx, kafka.Record{
		Topic:   event.Topic,
		Key:     []byte(event.AggregateID.String()),
		Value:   event.Payload,
		Headers: headers,
	})
	if err == nil {
		if err := s.repo.MarkOutboxEventPublished(ctx, event.ID); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "mark outbox published failed")
			s.log.ErrorContext(ctx, "mark outbox published failed", "error", err, "event_id", event.ID)
			return fmt.Errorf("mark outbox published: %w", err)
		}

		s.log.InfoContext(ctx, "outbox event published", "event_id", event.ID, "event_type", event.EventType)
		return nil
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, "publish outbox event failed")
	s.log.ErrorContext(ctx, "publish outbox event failed",
		"error", err, "event_id", event.ID, "event_type", event.EventType)

	backoff := time.Duration(min(int(event.AttemptCount)+1, 6)) * defaultRetryDelay
	if err := s.repo.MarkOutboxEventFailed(ctx, repository.MarkOutboxEventFailedParams{
		ID:            event.ID,
		NextAttemptAt: pgtype.Timestamptz{Time: time.Now().Add(backoff), Valid: true},
		LastError:     pgtype.Text{String: err.Error(), Valid: true},
	}); err != nil {
		span.RecordError(err)
		s.log.ErrorContext(ctx, "mark outbox failed failed", "error", err, "event_id", event.ID)
	}

	return fmt.Errorf("publish outbox event: %w", err)
}
