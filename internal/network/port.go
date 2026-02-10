package network

import (
	"fmt"
	"net"
	"sync"
)

// PortAllocator manages dynamic allocation of free host ports.
type PortAllocator struct {
	mu        sync.Mutex
	allocated map[int]struct{}
}

// NewPortAllocator creates a new port allocator.
func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		allocated: make(map[int]struct{}),
	}
}

// Allocate finds and reserves n free ports on the host.
// It uses the OS to find actually available ports.
func (a *PortAllocator) Allocate(n int) ([]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ports := make([]int, 0, n)
	for i := 0; i < n; i++ {
		port, err := findFreePort()
		if err != nil {
			// Release any ports we already allocated in this batch
			for _, p := range ports {
				delete(a.allocated, p)
			}
			return nil, fmt.Errorf("allocate port %d/%d: %w", i+1, n, err)
		}
		ports = append(ports, port)
		a.allocated[port] = struct{}{}
	}
	return ports, nil
}

// AllocateOne finds and reserves a single free port.
func (a *PortAllocator) AllocateOne() (int, error) {
	ports, err := a.Allocate(1)
	if err != nil {
		return 0, err
	}
	return ports[0], nil
}

// Release marks ports as no longer in use.
func (a *PortAllocator) Release(ports ...int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range ports {
		delete(a.allocated, p)
	}
}

// findFreePort asks the OS for an available port by binding to :0.
func findFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen on :0: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
