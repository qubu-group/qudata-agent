package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/qudata/agent/internal/agent"
	"github.com/qudata/agent/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	a, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create agent", "err", err)
		os.Exit(1)
	}

	if err := a.Run(ctx); err != nil {
		logger.Error("agent exited with error", "err", err)
		os.Exit(1)
	}

	logger.Info("agent stopped cleanly")
}
