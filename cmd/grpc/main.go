// Command grpc is expense's internal gRPC entry point (decision D2): it
// serves MemberService so identity can check approver assignments before
// committing a member removal. Wiring lives in internal/app.
package main

import (
	"log/slog"
	"os"

	"github.com/disillusioned-labs/expense/internal/app"
	"github.com/disillusioned-labs/expense/internal/config"
)

func main() {
	// Loaded here, not inside RunGRPC, so RunGRPC stays callable with a
	// config built in code.
	cfg, err := config.Load()
	if err != nil {
		// The real logger is built from cfg, so this failure has to report
		// through the default one.
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	cfg.Service.Name += "-grpc"

	if err := app.RunGRPC(cfg); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
