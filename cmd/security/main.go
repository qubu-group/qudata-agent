package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/security"
)

func main() {
	monitor := security.NewSecurityMonitor()

	monitor.SetAlertHandler(func(e security.MonitorEvent) {
		logger.Log(e.Level, "security monitor: %s - %s", e.Source, e.Message)
	})

	monitor.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	monitor.Stop()

	logger.LogInfo("security monitor stopped")
	time.Sleep(time.Second)
}
