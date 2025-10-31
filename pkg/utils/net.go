package utils

import (
	"fmt"
	"net"
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
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		LogWarn("failed to get public IP: %v", err)
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr()
	udpAddr, ok := localAddr.(*net.UDPAddr)
	if !ok {
		LogWarn("failed to cast to UDPAddr")
		return "127.0.0.1"
	}
	return udpAddr.IP.String()
}
