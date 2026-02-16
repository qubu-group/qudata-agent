package network

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
)

const (
	SSHPortMin = 10000
	SSHPortMax = 10099
	AppPortMin = 15001
	AppPortMax = 15300
)

type PortAllocator struct {
	mu        sync.Mutex
	allocated map[int]struct{}
}

func NewPortAllocator() *PortAllocator {
	return &PortAllocator{allocated: make(map[int]struct{})}
}

func (a *PortAllocator) AllocateSSHPort() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocateFromRange(SSHPortMin, SSHPortMax)
}

func (a *PortAllocator) AllocateAppPorts(n int) ([]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ports := make([]int, 0, n)
	for i := 0; i < n; i++ {
		p, err := a.allocateFromRange(AppPortMin, AppPortMax)
		if err != nil {
			for _, allocated := range ports {
				delete(a.allocated, allocated)
			}
			return nil, fmt.Errorf("allocate app port %d/%d: %w", i+1, n, err)
		}
		ports = append(ports, p)
	}
	return ports, nil
}

func (a *PortAllocator) AllocateOne() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocateFromRange(AppPortMin, AppPortMax)
}

func (a *PortAllocator) Release(ports ...int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range ports {
		delete(a.allocated, p)
	}
}

func (a *PortAllocator) allocateFromRange(min, max int) (int, error) {
	start := min + rand.Intn(max-min+1)
	for i := 0; i <= max-min; i++ {
		port := min + (start-min+i)%(max-min+1)
		if _, taken := a.allocated[port]; taken {
			continue
		}
		if !isPortFree(port) {
			continue
		}
		a.allocated[port] = struct{}{}
		return port, nil
	}
	return 0, fmt.Errorf("no free port in range %d-%d", min, max)
}

func isPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}
