package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/repository"
	ocrconsumer "github.com/disillusioned-labs/expense/internal/service/ocrconsumer"
	"github.com/disillusioned-labs/expense/internal/service/outbox"
	reminderservice "github.com/disillusioned-labs/expense/internal/service/reminder"
	"github.com/disillusioned-labs/platform/kafka"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/disillusioned-labs/platform/telemetry"

	migrations "github.com/disillusioned-labs/expense/db/migrations"

	"golang.org/x/sync/errgroup"
)

const (
	publishInterval = 1 * time.Second
	workerIDPrefix  = "outbox-worker-"
)

// RunWorker is the outbox publisher: it drains transactional outbox rows to
// Kafka. It is producer-only; consuming lives in RunConsumer (internal/app/consumer.go)
// so the two roles scale and restart independently.
func RunWorker(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := telemetry.NewLogger(cfg.Log.Level,
		telemetry.Format(cfg.Log.Format),
		telemetry.Env(cfg.Service.Env),
		telemetry.Service(cfg.Service.Name),
	)
	slog.SetDefault(log)
	log.Info("starting", "service", cfg.Service.Name, "role", "worker", "build", buildInfo())

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
	})
	if err != nil {
		return fmt.Errorf("connect kafka: %w", err)
	}
	defer kafkaClient.Close()
	producer := kafka.NewProducer(kafkaClient)

	publisher := outbox.NewOutboxService(repo, producer, log)
	workerID := workerIDPrefix + cfg.Service.InstanceID

	// The reminder worker needs live identity data (fresh approver emails),
	// so it only runs when it can reach identity's gRPC surface.
	var reminder reminderservice.Service
	if cfg.Remind.Enabled {
		identityClient, closeIdentity, err := newIdentityClient(ctx, cfg, log)
		if err != nil {
			return err
		}
		defer closeIdentity()
		reminder = reminderservice.NewService(repo, identityClient, cfg.Remind.ThresholdDays, cfg.Remind.BatchSize, log)
	}

	g, runCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ticker := time.NewTicker(publishInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return nil
			case <-ticker.C:
				if err := publisher.PublishPending(runCtx, workerID, 100); err != nil {
					log.Error("outbox publish tick failed", "error", err)
				}
			}
		}
	})

	if reminder != nil {
		g.Go(func() error {
			ticker := time.NewTicker(cfg.Remind.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return nil
				case <-ticker.C:
					// A failed tick is logged, not fatal: reminders resume
					// on the next tick and the outbox publisher keeps
					// draining regardless.
					if sent, err := reminder.RemindOnce(runCtx); err != nil {
						log.Error("approval reminder tick failed", "error", err)
					} else if sent > 0 {
						log.Info("approval reminders sent", "count", sent)
					}
				}
			}
		})
	}

	// The OCR sweep reconciles documents whose routed event never arrived;
	// it needs the gateway's status surface, so it only runs when the
	// gateway is configured.
	if cfg.Ocr.GatewayTarget != "" {
		gateway, closeGateway, err := newOcrGateway(ctx, cfg, log)
		if err != nil {
			return err
		}
		defer closeGateway()

		sweeper := ocrconsumer.NewSweeper(repo, gateway, log)

		g.Go(func() error {
			ticker := time.NewTicker(cfg.Ocr.SweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return nil
				case <-ticker.C:
					// A failed tick is logged, not fatal: the sweep is a
					// safety net, never on the critical path.
					swept, err := sweeper.SweepStale(runCtx, cfg.Ocr.SweepAfter, cfg.Ocr.SweepBatch)
					if err != nil {
						log.Error("ocr sweep tick failed", "error", err)
					} else if swept > 0 {
						log.Info("ocr sweep resolved stale documents", "count", swept)
					}
				}
			}
		})
	}

	<-runCtx.Done()
	log.Info("shutdown initiated", "cause", shutdownCause(ctx.Err() != nil))
	if err := g.Wait(); err != nil {
		return err
	}
	log.Info("shutdown complete")
	return nil
}
