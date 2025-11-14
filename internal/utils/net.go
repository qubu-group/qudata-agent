package utils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type portConfig struct {
	agentPort     int
	instancePorts []int
	nextPortIndex int
}

var config *portConfig

func InitNetwork() {
	portsEnv := os.Getenv("QUDATA_PORTS")
	LogWarn("Found env QUDATA_PORTS=", portsEnv)
	if portsEnv == "" {
		return
	}

	var allPorts []int
	parts := strings.Split(portsEnv, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				LogError("Invalid port range: %s", part)
				return
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				LogError("Invalid start port: %s", rangeParts[0])
				return
			}

			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				LogError("Invalid end port: %s", rangeParts[1])
				return
			}

			if start > end || start < 1 || end > 65535 {
				LogError("Invalid port range: %d-%d", start, end)
				return
			}

			for port := start; port <= end; port++ {
				allPorts = append(allPorts, port)
			}
		} else {
			port, err := strconv.Atoi(part)
			if err != nil || port < 1 || port > 65535 {
				LogError("Invalid port: %s", part)
				return
			}
			allPorts = append(allPorts, port)
		}
	}

	if len(allPorts) == 0 {
		LogError("No ports specified in QUDATA_PORTS")
		return
	}

	config = &portConfig{
		agentPort:     allPorts[0],
		instancePorts: allPorts[1:],
		nextPortIndex: 0,
	}

	LogInfo("Custom ports: agent=%d, instances=%d", config.agentPort, len(config.instancePorts))
}

func isPortAvailable(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func GetFreePort() int {
	if config != nil {
		if isPortAvailable(config.agentPort) {
			return config.agentPort
		}
		LogError("Configured agent port %d is not available", config.agentPort)
		return 0
	}

	l, err := net.Listen("tcp", ":0")
	if err != nil {
		LogWarn("Failed to get free port: %v", err)
		return 0
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		LogWarn("Failed to cast to TCPAddr")
		return 0
	}
	return addr.Port
}

func GetPortsRange(r int) (int, int) {
	if config != nil {
		if config.nextPortIndex >= len(config.instancePorts) {
			LogError("All configured ports have been allocated")
			return 0, 0
		}

		var availablePorts []int
		checkedCount := 0

		for i := config.nextPortIndex; i < len(config.instancePorts) && len(availablePorts) < r; i++ {
			port := config.instancePorts[i]
			checkedCount++

			if isPortAvailable(port) {
				availablePorts = append(availablePorts, port)
			}
		}

		config.nextPortIndex += checkedCount

		if len(availablePorts) == 0 {
			LogError("No available ports in configured range (checked %d ports)", checkedCount)
			return 0, 0
		}

		if len(availablePorts) < r {
			LogWarn("Requested %d ports, allocated %d available ports from configured range", r, len(availablePorts))
		}

		start := availablePorts[0]
		end := availablePorts[len(availablePorts)-1]

		LogInfo("Allocated %d ports: %d-%d (checked %d)", len(availablePorts), start, end, checkedCount)
		return start, end
	}

	const maxAttempts = 300
	const startPort = 10000

	for attempt := 0; attempt < maxAttempts; attempt++ {
		basePort := startPort + (attempt * r * 2)
		allFree := true

		for i := 0; i < r; i++ {
			if !isPortAvailable(basePort + i) {
				allFree = false
				break
			}
		}

		if allFree {
			return basePort, basePort + r - 1
		}
	}

	return 0, 0
}

func GetPublicIP() string {
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
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			ip := strings.TrimSpace(string(body))
			if ip != "" && net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	LogWarn("Failed to get public IP from all services")
	return "127.0.0.1"
}
