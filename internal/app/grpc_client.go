package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/ocr"
	platformgrpc "github.com/disillusioned-labs/platform/grpc"
)

func newIdentityClient(_ context.Context, cfg *config.Config, log *slog.Logger) (contract.IdentityClient, func(), error) {
	opts := []platformgrpc.Option{
		platformgrpc.WithUnaryTimeout(cfg.GRPCClient.Timeout),
		platformgrpc.WithMaxRecvMsgSize(cfg.GRPCClient.MaxRecvMsgSize),
		platformgrpc.WithMaxSendMsgSize(cfg.GRPCClient.MaxSendMsgSize),
		platformgrpc.WithLogger(log),
		// Propagate x-request-id from incoming HTTP metadata to identity.
		platformgrpc.WithUnaryClientInterceptor(
			platformgrpc.UnaryForwardMetadataClient([]string{"x-request-id"}),
		),
	}

	if cfg.GRPCClient.TLS.Enabled {
		tlsConfig, err := platformgrpc.NewTLSConfig(
			cfg.GRPCClient.TLS.CAFile,
			cfg.GRPCClient.TLS.CertFile,
			cfg.GRPCClient.TLS.KeyFile,
			cfg.GRPCClient.TLS.ServerName,
			cfg.GRPCClient.TLS.MutualTLS,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("build identity gRPC TLS config: %w", err)
		}
		opts = append(opts, platformgrpc.WithTLS(tlsConfig))
	}
	if cfg.Identity.GRPCServiceToken != "" {
		credentials, err := platformgrpc.NewServiceTokenCredentials(
			cfg.Identity.GRPCServiceToken,
			cfg.GRPCClient.TLS.Enabled,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("configure identity gRPC service credentials: %w", err)
		}
		opts = append(opts, platformgrpc.WithPerRPCCredentials(credentials))
	}

	client, err := platformgrpc.NewClient(cfg.Identity.GRPCTarget, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create identity gRPC client: %w", err)
	}

	identityClient := contract.NewGRPCIdentityClient(client, log)
	cleanup := func() { _ = client.Close() }

	return identityClient, cleanup, nil
}

func newOcrGatewaySubmitter(ctx context.Context, cfg *config.Config, log *slog.Logger) (ocr.Submitter, func(), error) {
	if cfg.Ocr.GatewayTarget == "" {
		return ocr.NewNoopSubmitter(log), func() {}, nil
	}

	gateway, cleanup, err := newOcrGateway(ctx, cfg, log)
	if err != nil {
		return nil, nil, err
	}

	return gateway, cleanup, nil
}

// newOcrGateway returns the concrete gateway adapter, whose submit and status
// surfaces share one connection.
func newOcrGateway(_ context.Context, cfg *config.Config, log *slog.Logger) (*contract.OcrGateway, func(), error) {
	opts := []platformgrpc.Option{
		platformgrpc.WithUnaryTimeout(cfg.GRPCClient.Timeout),
		platformgrpc.WithMaxRecvMsgSize(cfg.GRPCClient.MaxRecvMsgSize),
		platformgrpc.WithMaxSendMsgSize(cfg.GRPCClient.MaxSendMsgSize),
		platformgrpc.WithLogger(log),
	}
	if cfg.GRPCClient.TLS.Enabled {
		tlsConfig, err := platformgrpc.NewTLSConfig(
			cfg.GRPCClient.TLS.CAFile,
			cfg.GRPCClient.TLS.CertFile,
			cfg.GRPCClient.TLS.KeyFile,
			cfg.GRPCClient.TLS.ServerName,
			cfg.GRPCClient.TLS.MutualTLS,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("build ocr-gateway gRPC TLS config: %w", err)
		}
		opts = append(opts, platformgrpc.WithTLS(tlsConfig))
	}

	client, err := platformgrpc.NewClient(cfg.Ocr.GatewayTarget, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create ocr-gateway gRPC client: %w", err)
	}

	return contract.NewOcrGateway(client, log), func() { _ = client.Close() }, nil
}
