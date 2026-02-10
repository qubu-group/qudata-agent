package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/qudata/agent/internal/agent"
	"github.com/qudata/agent/internal/config"
)

func main() {
	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := config.NewLogger(cfg, "agent")
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		os.Exit(1)
	}

	logger.Info("starting qudata-agent",
		"version", config.Version,
		"build_time", config.BuildTime,
		"debug", cfg.Debug,
	)

	// Create context with signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	// Create and run agent
	a, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create agent", "err", err)
		os.Exit(1)
	}

	if err := a.Run(ctx); err != nil {
		logger.Error("agent exited with error", "err", err)
		os.Exit(1)
	}

	// Also log to stderr for systemd journal visibility
	slog.SetDefault(logger)
	logger.Info("agent stopped cleanly")
}
