package security

import (
	"sync"
	"time"
)

type FanotifyEventType uint32

const (
	EventAccess FanotifyEventType = 1 << iota
	EventModify
	EventClose
	EventOpen
)

type FanotifyEvent struct {
	Path      string
	Type      FanotifyEventType
	Timestamp time.Time
	PID       int
}

type Fanotify struct {
	mu       sync.Mutex
	running  bool
	paths    []string
	eventsCh chan FanotifyEvent
	stopCh   chan struct{}
}

func NewFanotify() *Fanotify {
	return &Fanotify{
		eventsCh: make(chan FanotifyEvent, 100),
		stopCh:   make(chan struct{}),
	}
}

func (f *Fanotify) AddWatch(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, path)
}

func (f *Fanotify) Start() {
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return
	}
	f.running = true
	f.mu.Unlock()

	go f.run()
}

func (f *Fanotify) run() {
	<-f.stopCh
}

func (f *Fanotify) Events() <-chan FanotifyEvent {
	return f.eventsCh
}

func (f *Fanotify) Stop() {
	f.mu.Lock()
	if !f.running {
		f.mu.Unlock()
		return
	}
	f.running = false
	f.mu.Unlock()

	close(f.stopCh)
	close(f.eventsCh)
}

func (f *Fanotify) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
