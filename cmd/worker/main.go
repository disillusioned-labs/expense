package main

import (
	"log/slog"
	"os"

	"github.com/disillusioned-labs/expense/internal/app"
	"github.com/disillusioned-labs/expense/internal/config"
)

func main() {
	// Loaded here, not inside RunWorker, so RunWorker stays callable with a config built in code.
	cfg, err := config.Load()
	if err != nil {
		// The real logger is built from cfg, so this failure has to report
		// through the default one.
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	cfg.Service.Name += "-worker"

	if err := app.RunWorker(cfg); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
