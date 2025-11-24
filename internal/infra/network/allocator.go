package network

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

type portConfig struct {
	agentPort     int
	instancePorts []int
	nextPortIndex int
}

// Allocator управляет выделением портов для агента и инстансов.
type Allocator struct {
	logger    impls.Logger
	cfg       *portConfig
	mu        sync.Mutex
	agentPort int
}

func NewAllocator(logger impls.Logger) *Allocator {
	return &Allocator{logger: logger}
}

// Configure разбирает значение QUDATA_PORTS.
func (a *Allocator) Configure(raw string) {
	if raw == "" {
		a.logger.Info("QUDATA_PORTS not set, using dynamic port allocation")
		return
	}

	var portsList []int
	parts := strings.Split(raw, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				a.logger.Error("invalid port range: %s", part)
				return
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				a.logger.Error("invalid start port: %s", rangeParts[0])
				return
			}
			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				a.logger.Error("invalid end port: %s", rangeParts[1])
				return
			}

			if start > end || start < 1 || end > 65535 {
				a.logger.Error("invalid port range: %d-%d", start, end)
				return
			}

			for port := start; port <= end; port++ {
				portsList = append(portsList, port)
			}
			continue
		}

		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			a.logger.Error("invalid port: %s", part)
			return
		}
		portsList = append(portsList, port)
	}

	if len(portsList) == 0 {
		a.logger.Error("no impls specified in QUDATA_PORTS")
		return
	}

	a.cfg = &portConfig{
		agentPort:     portsList[0],
		instancePorts: portsList[1:],
	}
	a.logger.Info("Custom impls configured: agent=%d, instances=%d", a.cfg.agentPort, len(a.cfg.instancePorts))
}

func (a *Allocator) AgentPort() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.agentPort != 0 {
		return a.agentPort, nil
	}

	if a.cfg != nil {
		if isPortAvailable(a.cfg.agentPort) {
			a.agentPort = a.cfg.agentPort
			return a.agentPort, nil
		}
		return 0, fmt.Errorf("configured agent port %d is busy", a.cfg.agentPort)
	}

	port, err := findEphemeralPort()
	if err != nil {
		return 0, err
	}
	a.agentPort = port
	return port, nil
}

func (a *Allocator) Allocate(count int) ([]int, error) {
	if count <= 0 {
		return nil, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg != nil {
		return a.allocateFromConfig(count)
	}
	return a.allocateDynamically(count)
}

func (a *Allocator) allocateFromConfig(count int) ([]int, error) {
	if a.cfg == nil || len(a.cfg.instancePorts) == 0 {
		return nil, fmt.Errorf("custom port configuration is empty")
	}

	var available []int
	checked := 0

	for i := a.cfg.nextPortIndex; i < len(a.cfg.instancePorts) && len(available) < count; i++ {
		port := a.cfg.instancePorts[i]
		checked++
		if isPortAvailable(port) {
			available = append(available, port)
		}
	}

	a.cfg.nextPortIndex += checked

	if len(available) == 0 {
		return nil, fmt.Errorf("no available impls in configured range (checked %d)", checked)
	}

	if len(available) < count {
		a.logger.Warn("requested %d impls, allocated %d", count, len(available))
	}

	a.logger.Info("Allocated %d custom impls starting from %d", len(available), available[0])
	return available, nil
}

func (a *Allocator) allocateDynamically(count int) ([]int, error) {
	const (
		maxAttempts = 300
		startPort   = 10000
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		base := startPort + (attempt * count * 2)
		success := true
		for i := 0; i < count; i++ {
			if !isPortAvailable(base + i) {
				success = false
				break
			}
		}
		if success {
			ports := make([]int, count)
			for i := 0; i < count; i++ {
				ports[i] = base + i
			}
			return ports, nil
		}
	}
	return nil, fmt.Errorf("failed to allocate %d impls dynamically", count)
}

func isPortAvailable(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func findEphemeralPort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unable to resolve TCP addr")
	}
	return addr.Port, nil
}

// PublicIP возвращает внешний IP-адрес через несколько сервисов.
func PublicIP() string {
	services := []string{
		"https://api.ipify.org",
		"https://icanhazip.com",
		"https://ifconfig.me",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, service := range services {
		resp, err := client.Get(service)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if ip != "" && net.ParseIP(ip) != nil {
			return ip
		}
	}

	return "127.0.0.1"
}
