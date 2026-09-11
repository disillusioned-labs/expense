package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/contract"
	platformgrpc "github.com/disillusioned-labs/platform/grpc"
)

// newIdentityClient creates the gRPC client to the identity service.
// Returns (client, cleanup, error). Caller must defer cleanup.
func newIdentityClient(ctx context.Context, cfg *config.Config, log *slog.Logger) (contract.IdentityClient, func(), error) {
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
		// TODO: build *tls.Config from GRPCTLSConfig fields when TLS is enabled.
		// opts = append(opts, platformgrpc.WithTLS(tlsConfig))
	}

	client, err := platformgrpc.NewClient(cfg.GRPCClient.Target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create identity gRPC client: %w", err)
	}

	identityClient := contract.NewGRPCIdentityClient(client, log)
	cleanup := func() { _ = client.Close() }

	return identityClient, cleanup, nil
}
