package utils

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	logFile *os.File
	logMu   sync.Mutex
)

func init() {
	os.MkdirAll("/var/log/qudata", 0755)
	f, err := os.OpenFile("/var/log/qudata/agent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	logFile = f
}

func Log(level, msg string, args ...interface{}) {
	if logFile == nil {
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(msg, args...)
	fmt.Fprintf(logFile, "[%s] %s: %s\n", timestamp, level, message)
	logFile.Sync()
}

func LogError(msg string, args ...interface{}) {
	Log("ERROR", msg, args...)
}

func LogWarn(msg string, args ...interface{}) {
	Log("WARN", msg, args...)
}

func LogInfo(msg string, args ...interface{}) {
	Log("INFO", msg, args...)
}
