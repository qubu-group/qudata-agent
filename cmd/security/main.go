package main

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/security"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	monitor := security.NewSecurityMonitor()

	monitor.SetAlertHandler(func(e security.MonitorEvent) {
		utils.Log(e.Level, "security monitor: %s - %s", e.Source, e.Message)
	})

	monitor.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	monitor.Stop()

	utils.LogInfo("security monitor stopped")
	time.Sleep(time.Second)
}
