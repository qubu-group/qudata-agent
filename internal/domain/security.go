package domain

import "time"

// MonitorEvent фиксирует событие безопасности.
type MonitorEvent struct {
	Time    time.Time
	Source  string
	Message string
	Level   string
}
