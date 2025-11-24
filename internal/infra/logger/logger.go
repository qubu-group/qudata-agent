package logger

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	logFile  *os.File
	initOnce sync.Once
	logMu    sync.Mutex
)

// FileLogger реализует impls.Logger и пишет логи в /var/log/qudata/agent.log.
type FileLogger struct{}

func NewFileLogger() *FileLogger {
	initOnce.Do(initLogFile)
	return &FileLogger{}
}

func initLogFile() {
	if logFile != nil {
		return
	}
	_ = os.MkdirAll("/var/log/qudata", 0o755)
	f, err := os.OpenFile("/var/log/qudata/agent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	logFile = f
}

func (l *FileLogger) Info(msg string, args ...any) {
	l.write("INFO", msg, args...)
}

func (l *FileLogger) Warn(msg string, args ...any) {
	l.write("WARN", msg, args...)
}

func (l *FileLogger) Error(msg string, args ...any) {
	l.write("ERROR", msg, args...)
}

func (l *FileLogger) write(level, msg string, args ...any) {
	initOnce.Do(initLogFile)
	if logFile == nil {
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(msg, args...)
	fmt.Fprintf(logFile, "[%s] %s: %s\n", timestamp, level, message)
	_ = logFile.Sync()
}

var defaultLogger = NewFileLogger()

func Log(level, msg string, args ...any) {
	defaultLogger.write(level, msg, args...)
}

func LogInfo(msg string, args ...any) {
	Log("INFO", msg, args...)
}

func LogWarn(msg string, args ...any) {
	Log("WARN", msg, args...)
}

func LogError(msg string, args ...any) {
	Log("ERROR", msg, args...)
}
