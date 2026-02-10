package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcessInfo contains information about a running QEMU process.
type ProcessInfo struct {
	PID       int
	QMPSocket string
	VMID      string
}

// FindQEMUProcessBySocket searches /proc for a QEMU process using the given QMP socket.
// Returns the PID if found, or 0 if not found.
func FindQEMUProcessBySocket(qmpSocket string) (int, error) {
	procDir, err := os.Open("/proc")
	if err != nil {
		return 0, fmt.Errorf("open /proc: %w", err)
	}
	defer procDir.Close()

	entries, err := procDir.Readdirnames(-1)
	if err != nil {
		return 0, fmt.Errorf("read /proc: %w", err)
	}

	for _, entry := range entries {
		// Skip non-numeric entries
		pid, err := strconv.Atoi(entry)
		if err != nil {
			continue
		}

		// Read command line
		cmdlinePath := filepath.Join("/proc", entry, "cmdline")
		cmdline, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		// Check if this is a QEMU process with our socket
		cmdlineStr := string(cmdline)
		if !strings.Contains(cmdlineStr, "qemu-system") {
			continue
		}

		if strings.Contains(cmdlineStr, qmpSocket) {
			return pid, nil
		}
	}

	return 0, nil
}

// FindOrphanVMs scans the run directory for QMP sockets and finds corresponding QEMU processes.
// Returns a list of VM IDs that have running QEMU processes but no agent managing them.
func FindOrphanVMs(runDir string) ([]ProcessInfo, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read run dir: %w", err)
	}

	var orphans []ProcessInfo

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".qmp") {
			continue
		}

		vmID := strings.TrimSuffix(name, ".qmp")
		qmpSocket := filepath.Join(runDir, name)

		// Check if there's a QEMU process using this socket
		pid, err := FindQEMUProcessBySocket(qmpSocket)
		if err != nil {
			continue
		}

		if pid != 0 {
			orphans = append(orphans, ProcessInfo{
				PID:       pid,
				QMPSocket: qmpSocket,
				VMID:      vmID,
			})
		} else {
			// No process found - clean up stale socket
			_ = os.Remove(qmpSocket)
		}
	}

	return orphans, nil
}

// ProcessExists checks if a process with the given PID exists.
func ProcessExists(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Sending signal 0 checks if process exists without actually sending a signal
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds. We need to send signal 0.
	err = process.Signal(os.Signal(nil))
	return err == nil
}

// KillProcess sends SIGKILL to a process.
func KillProcess(pid int) error {
	if pid <= 0 {
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return process.Kill()
}

// ReadPIDFromSocket reads /proc to find the PID of process listening on a Unix socket.
// This is used as a fallback when we don't have the PID stored.
func ReadPIDFromSocket(socketPath string) (int, error) {
	return FindQEMUProcessBySocket(socketPath)
}
