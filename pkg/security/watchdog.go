package security

import (
	"context"
	"os"
	"sync"
	"time"
)

type Watchdog struct {
	mu       sync.Mutex
	running  bool
	interval time.Duration
	timeout  time.Duration
	lastPing time.Time
	cancel   context.CancelFunc
	onFail   func()
}

func NewWatchdog(timeout time.Duration, onFail func()) *Watchdog {
	return &Watchdog{
		timeout:  timeout,
		interval: timeout / 3,
		onFail:   onFail,
	}
}

func (w *Watchdog) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.lastPing = time.Now()
	w.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	go w.run(ctx)
}

func (w *Watchdog) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			elapsed := time.Since(w.lastPing)
			w.mu.Unlock()

			if elapsed > w.timeout {
				if w.onFail != nil {
					w.onFail()
				}
			}
		}
	}
}

func (w *Watchdog) Ping() {
	w.mu.Lock()
	w.lastPing = time.Now()
	w.mu.Unlock()
}

func (w *Watchdog) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	w.mu.Unlock()

	if w.cancel != nil {
		w.cancel()
	}
}

func (w *Watchdog) IsAlive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Since(w.lastPing) < w.timeout
}

var defaultWatchdog *Watchdog

func InitWatchdog(timeout time.Duration) {
	defaultWatchdog = NewWatchdog(timeout, func() {
		os.Exit(1)
	})
	defaultWatchdog.Start()
}

func Ping() {
	if defaultWatchdog != nil {
		defaultWatchdog.Ping()
	}
}

func StopWatchdog() {
	if defaultWatchdog != nil {
		defaultWatchdog.Stop()
	}
}
