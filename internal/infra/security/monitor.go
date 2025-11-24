package security

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

type MonitorEvent struct {
	Time    time.Time
	Source  string
	Message string
	Level   string // info, warn, critical
}

type Monitor struct {
	mu      sync.Mutex
	events  []MonitorEvent
	stopCh  chan struct{}
	wg      sync.WaitGroup
	onAlert func(MonitorEvent)
}

func NewSecurityMonitor() *Monitor {
	return &Monitor{
		stopCh: make(chan struct{}),
		onAlert: func(e MonitorEvent) {
			logger.Log(e.Level, "security: %s - %s", e.Source, e.Message)
		},
	}
}

func (sm *Monitor) SetAlertHandler(fn func(MonitorEvent)) {
	sm.onAlert = fn
}

func (sm *Monitor) Start() {
	sm.wg.Add(2)
	go sm.watchFanotify()
	go sm.watchAuditd()
}

func (sm *Monitor) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

func (sm *Monitor) watchFanotify() {
	defer sm.wg.Done()
	cmd := exec.Command("journalctl", "-f", "-u", "fanotify")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-sm.stopCh:
			cmd.Process.Kill()
			return
		default:
			line := scanner.Text()
			if strings.Contains(line, "DENY") {
				sm.record("fanotify", line, "warn")
			}
		}
	}
}

func (sm *Monitor) watchAuditd() {
	defer sm.wg.Done()
	file, err := os.Open("/var/log/audit/audit.log")
	if err != nil {
		sm.record("auditd", fmt.Sprintf("cannot open audit log: %v", err), "info")
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		select {
		case <-sm.stopCh:
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				time.Sleep(1 * time.Second)
				continue
			}
			if strings.Contains(line, "avc:") || strings.Contains(line, "apparmor=") {
				sm.record("auditd", strings.TrimSpace(line), "warn")
			}
		}
	}
}

func (sm *Monitor) record(source, message, level string) {
	e := MonitorEvent{
		Time:    time.Now(),
		Source:  source,
		Message: message,
		Level:   level,
	}
	sm.mu.Lock()
	sm.events = append(sm.events, e)
	sm.mu.Unlock()
	if sm.onAlert != nil {
		sm.onAlert(e)
	}
}
