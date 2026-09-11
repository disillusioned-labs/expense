package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service/ocrconsumer"
	"github.com/disillusioned-labs/platform/kafka"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/disillusioned-labs/platform/telemetry"

	migrations "github.com/disillusioned-labs/expense/db/migrations"
)

// RunConsumer is the OCR consumer entry point: it listens for
// document.processed events and turns them into draft transactions. It is
// its own binary so the consumer scales and restarts independently of the
// outbox publisher; both are safe to run multi-replica (SKIP LOCKED claims
// for the outbox, consumer groups for this loop).
func RunConsumer(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := telemetry.NewLogger(cfg.Log.Level,
		telemetry.Format(cfg.Log.Format),
		telemetry.Env(cfg.Service.Env),
		telemetry.Service(cfg.Service.Name),
	)
	slog.SetDefault(log)
	log.Info("starting", "service", cfg.Service.Name, "role", "consumer", "build", buildInfo())

	// Unlike the worker, a consumer with no group configured is a misdeployment,
	// not a no-op: the whole point of this binary is to consume.
	if cfg.Kafka.Consumer.Group == "" {
		return fmt.Errorf("consumer requires KAFKA_CONSUMER_GROUP and KAFKA_CONSUMER_TOPICS to be set")
	}

	pool, err := postgres.NewPool(ctx, cfg.Postgres.DSN,
		postgres.MaxConns(cfg.Postgres.MaxConns),
		postgres.MinConns(1),
	)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	if cfg.Postgres.Migrate {
		if err := postgres.Migrate(ctx, pool, migrations.FS, log); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}
	repo := repository.NewStore(pool)

	kafkaClient, err := kafka.New(ctx, kafka.KafkaConfig{
		Brokers:     cfg.Kafka.Brokers,
		ClientID:    cfg.Kafka.ClientID,
		PingTimeout: cfg.Kafka.PingTimeout,
		Producer: kafka.ProducerConfig{
			RecordRetries:         cfg.Kafka.Producer.RecordRetries,
			RecordDeliveryTimeout: cfg.Kafka.Producer.RecordDeliveryTimeout,
		},
		Consumer: kafka.ConsumerConfig{
			Group:    cfg.Kafka.Consumer.Group,
			Topics:   cfg.Kafka.Consumer.Topics,
			DLQTopic: cfg.Kafka.Consumer.DLQTopic,
			Retry: kafka.RetryConfig{
				MaxAttempts:  cfg.Kafka.Consumer.Retry.MaxAttempts,
				InitialDelay: cfg.Kafka.Consumer.Retry.InitialDelay,
				MaxDelay:     cfg.Kafka.Consumer.Retry.MaxDelay,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("connect kafka: %w", err)
	}
	defer kafkaClient.Close()

	producer := kafka.NewProducer(kafkaClient)
	consumer := kafka.NewConsumer(kafkaClient)

	log.Info("ocr consumer listening",
		"group", cfg.Kafka.Consumer.Group,
		"topics", cfg.Kafka.Consumer.Topics,
		"dlq_topic", cfg.Kafka.Consumer.DLQTopic,
	)
	err = runOCRConsumer(ctx, repo, consumer, producer, cfg, log)
	if ctx.Err() != nil {
		log.Info("shutdown initiated", "cause", shutdownCause(ctx.Err() != nil))
		log.Info("shutdown complete")
		return nil
	}
	return err
}

func runOCRConsumer(
	ctx context.Context,
	repo repository.Store,
	consumer *kafka.Consumer,
	producer kafka.Producer,
	cfg *config.Config,
	log *slog.Logger,
) error {
	dlq := kafka.NewDLQPublisher(producer, cfg.Kafka.Consumer.DLQTopic, log)
	svc := ocrconsumer.NewService(repo, log)

	for {
		records, err := consumer.Poll(ctx)
		if err != nil {
			return fmt.Errorf("poll document.processed: %w", err)
		}
		for _, rec := range records {
			if err := handleOCRRecord(ctx, svc, dlq, rec, log); err != nil {
				log.Error("ocr consumer record failed", "error", err,
					"topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset)
			}
			if err := consumer.CommitRecords(ctx, rec); err != nil {
				return fmt.Errorf("commit document.processed offset: %w", err)
			}
		}
	}
}

func handleOCRRecord(ctx context.Context, svc ocrconsumer.Service, dlq *kafka.DLQPublisher, rec kafka.Record, log *slog.Logger) error {
	processCtx := ctx
	if rec.Context != nil {
		var cancel context.CancelFunc
		processCtx, cancel = context.WithCancel(rec.Context)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}

	msg, err := ocrconsumer.ParseProcessed(rec.Value)
	if err != nil {
		return deadLetter(ctx, dlq, rec, err, log)
	}
	if err := svc.Handle(processCtx, msg); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(processCtx.Err(), context.Canceled) {
			return err
		}
		return deadLetter(ctx, dlq, rec, err, log)
	}
	return nil
}

func deadLetter(ctx context.Context, dlq *kafka.DLQPublisher, rec kafka.Record, cause error, log *slog.Logger) error {
	err := dlq.Publish(ctx, rec, kafka.DLQMetadata{
		SourceTopic:     rec.Topic,
		SourcePartition: rec.Partition,
		SourceOffset:    rec.Offset,
		Attempt:         1,
		ErrorType:       "ocr_consumer_error",
		ErrorCode:       "OCR_CONSUMER_ERROR",
	})
	if err != nil {
		log.Error("dead-letter publish failed; record will be redelivered",
			"error", err, "cause", cause, "topic", rec.Topic, "offset", rec.Offset)
		return err
	}
	log.Warn("record dead-lettered", "cause", cause, "topic", rec.Topic, "offset", rec.Offset)
	return nil
}
