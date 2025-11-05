package utils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func GetFreePort() int {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		LogWarn("failed to get free port: %v", err)
		return 0
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		LogWarn("failed to cast to TCPAddr")
		return 0
	}
	return addr.Port
}

func GetPortsRange(r int) (int, int) {
	const maxAttempts = 300
	const startPort = 10000

	for attempt := 0; attempt < maxAttempts; attempt++ {
		basePort := startPort + (attempt * r * 2)
		allFree := true

		for i := 0; i < r; i++ {
			port := basePort + i
			l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if err != nil {
				allFree = false
				break
			}
			l.Close()
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

	LogWarn("failed to get public IP from all services")
	return "127.0.0.1"
}
