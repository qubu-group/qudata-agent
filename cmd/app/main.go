package main

import (
	"context"
	"log"

	agentapp "github.com/magicaleks/qudata-agent-alpha/internal/app/agent"
)

func main() {
	ctx := context.Background()

	app, err := agentapp.NewApplication(ctx)
	if err != nil {
		log.Fatalf("failed to init application: %v", err)
	}

	if err := app.Run(ctx); err != nil {
		log.Fatalf("application stopped: %v", err)
	}
}
