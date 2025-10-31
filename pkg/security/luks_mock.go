//go:build !linux || !cgo

package security

import (
	"os"
	"sync"
)

var mgr = &volumeManager{}

type volumeManager struct {
	mu         sync.Mutex
	active     bool
	mountPoint string
}

type VolumeConfig struct {
	MountPoint string
	SizeMB     int64
	Key        string
}

func CreateVolume(config VolumeConfig) string {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if mgr.active {
		return ""
	}

	if config.MountPoint == "" {
		return ""
	}

	if err := os.MkdirAll(config.MountPoint, 0700); err != nil {
		return ""
	}

	mgr.active = true
	mgr.mountPoint = config.MountPoint

	return "mock-key"
}

func DeleteVolume() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if !mgr.active {
		return
	}

	os.RemoveAll(mgr.mountPoint)

	mgr.active = false
	mgr.mountPoint = ""
}

func IsActive() bool {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.active
}

func GetMountPoint() string {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if !mgr.active {
		return ""
	}
	return mgr.mountPoint
}
