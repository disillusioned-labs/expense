package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/server"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/disillusioned-labs/platform/telemetry"

	migrations "github.com/disillusioned-labs/expense/db/migrations"

	"golang.org/x/sync/errgroup"
)

const grpcOtelFlushTimeout = 5 * time.Second

// RunGRPC boots the internal gRPC server (decision D2, ExpenseService) with
// the given configuration and blocks until the process is told to stop. The
// caller owns loading and validating cfg (see cmd/grpc).
func RunGRPC(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := telemetry.NewLogger(cfg.Log.Level,
		telemetry.Format(cfg.Log.Format),
		telemetry.Env(cfg.Service.Env),
		telemetry.Service(cfg.Service.Name),
	)
	slog.SetDefault(log)
	log.Info("starting", "service", cfg.Service.Name, "build", buildInfo(), "transport", "grpc")

	// -------------------------------------------------------------------------
	// Telemetry
	// -------------------------------------------------------------------------
	otelOpts := []telemetry.Option{telemetry.WithBuild(version, commit)}
	if cfg.OTel.TracesEnabled() {
		sampler, err := telemetry.NewSampler(cfg.OTel.TracesSampler, cfg.OTel.TracesSamplerArg)
		if err != nil {
			return fmt.Errorf("configure trace sampler: %w", err)
		}
		otelOpts = append(otelOpts, telemetry.WithTracing(cfg.OTel.TraceEndpoint(), sampler))
	}
	if cfg.OTel.MetricsEnabled() {
		otelOpts = append(otelOpts, telemetry.WithMetrics(
			cfg.OTel.MetricEndpoint(), cfg.OTel.MetricExportInterval(),
		))
	}
	shutdownOtel, err := telemetry.Setup(ctx, cfg.Service.Name, cfg.Service.Env, otelOpts...)
	if err != nil {
		return fmt.Errorf("setup telemetry: %w", err)
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), grpcOtelFlushTimeout)
		defer cancel()
		if err := shutdownOtel(flushCtx); err != nil {
			log.Error("otel shutdown failed", "error", err)
		}
	}()

	// -------------------------------------------------------------------------
	// PostgreSQL
	// -------------------------------------------------------------------------
	pool, err := postgres.NewPool(ctx, cfg.Postgres.DSN,
		postgres.MaxConns(cfg.Postgres.MaxConns),
		postgres.MinConns(cfg.Postgres.MinConns),
		postgres.MaxConnLifetime(cfg.Postgres.MaxConnLifetime),
		postgres.QueryExecMode(cfg.Postgres.QueryExecMode),
	)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	log.Info("connected to postgres", "postgres", cfg.Postgres)

	if cfg.Postgres.Migrate {
		if err := postgres.Migrate(ctx, pool, migrations.FS, log); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	// -------------------------------------------------------------------------
	// Dependencies
	// -------------------------------------------------------------------------
	identityClient, closeIdentity, err := newIdentityClient(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeIdentity()

	repo := repository.NewStore(pool)
	authorizer := authz.NewAuthorizer(repo, identityClient, log)
	approvalSvc := approvalservice.NewApprovalService(repo, authorizer, identityClient, log)

	// -------------------------------------------------------------------------
	// gRPC server
	// -------------------------------------------------------------------------
	grpcSrv, err := server.NewGRPC(cfg, log, approvalSvc)
	if err != nil {
		return fmt.Errorf("create grpc server: %w", err)
	}

	pprofSrv := server.NewPprofServer(cfg.Pprof.Enabled, cfg.Pprof.Port, log)

	// -------------------------------------------------------------------------
	// Serve
	// -------------------------------------------------------------------------
	g, runCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := grpcSrv.Start(); err != nil {
			return fmt.Errorf("grpc listener: %w", err)
		}
		return nil
	})
	if pprofSrv != nil {
		g.Go(func() error {
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("pprof listener: %w", err)
			}
			return nil
		})
	}

	<-runCtx.Done()
	log.Info("shutdown initiated", "cause", shutdownCause(ctx.Err() != nil))
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	grpcSrv.BeginDrain()
	if err := grpcSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("grpc graceful shutdown: %w", err)
	}
	if pprofSrv != nil {
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("pprof shutdown: %w", err)
		}
	}
	if err := g.Wait(); err != nil {
		return err
	}

	log.Info("shutdown complete")
	return nil
}
