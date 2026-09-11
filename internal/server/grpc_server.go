package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/disillusioned-labs/expense/internal/config"
	grpchandler "github.com/disillusioned-labs/expense/internal/handler/grpc"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
	expensepb "github.com/disillusioned-labs/platform/contract/expense"
	platformgrpc "github.com/disillusioned-labs/platform/grpc"
)

// GRPCServer assembles the expense gRPC listener. It serves only the
// internal ExpenseService (decision D2); the public surface stays HTTP.
type GRPCServer struct {
	grpc *platformgrpc.Server
	log  *slog.Logger
	cfg  *config.Config
}

// NewGRPC builds the gRPC server and registers the ExpenseService handler.
func NewGRPC(cfg *config.Config, log *slog.Logger, approvals approvalservice.ApprovalService) (*GRPCServer, error) {
	grpcServer, err := platformgrpc.NewServer(
		platformgrpc.WithMaxRecvMsgSize(cfg.GRPC.MaxRecvMsgSize),
		platformgrpc.WithMaxSendMsgSize(cfg.GRPC.MaxSendMsgSize),
		platformgrpc.WithMaxHeaderSize(cfg.GRPC.MaxHeaderSize),
		platformgrpc.WithLogger(log),
		platformgrpc.WithUnaryServerInterceptor(platformgrpc.UnaryRequestIDServer(log)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc server: %w", err)
	}

	expenseServer := grpchandler.NewExpenseServer(approvals, log)
	expensepb.RegisterExpenseServiceServer(grpcServer.GRPC(), expenseServer)

	return &GRPCServer{grpc: grpcServer, log: log, cfg: cfg}, nil
}

// BeginDrain flips the gRPC health to NOT_SERVING before Shutdown, so
// callers stop being routed here while in-flight RPCs drain.
func (s *GRPCServer) BeginDrain() {
	s.grpc.Health().SetNotServing("")
}

// Start blocks until the listener fails or Shutdown is called.
func (s *GRPCServer) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.GRPC.ServerPort)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}

	s.log.Info("grpc server listening", "addr", addr)
	if err := s.grpc.Serve(listener); err != nil {
		return fmt.Errorf("grpc server: %w", err)
	}
	return nil
}

// Shutdown drains in-flight RPCs until ctx expires, then closes.
func (s *GRPCServer) Shutdown(ctx context.Context) error {
	s.log.Info("shutting down grpc server")
	return s.grpc.Stop(ctx)
}
