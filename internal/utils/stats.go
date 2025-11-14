package utils

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	lastInetIn  int64
	lastInetOut int64
	lastNetTime int64
	netMutex    sync.Mutex
)

func GetCPUUtil() float64 {
	cmd := exec.Command("sh", "-c", "top -bn1 | grep 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1}'")
	output, err := cmd.Output()
	if err != nil {
		LogWarn("Get CPU Utilization: %s", err.Error())
		return 0.0
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		LogWarn("Get CPU Utilization: %s", err.Error())
		return 0.0
	}
	return util
}

func GetRAMUtil() float64 {
	cmd := exec.Command("free", "-b")
	output, err := cmd.Output()
	if err != nil {
		LogWarn("Get RAM Utilization: %s", err.Error())
		return 0.0
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				total, err1 := strconv.ParseFloat(fields[1], 64)
				used, err2 := strconv.ParseFloat(fields[2], 64)
				if err1 == nil && err2 == nil && total > 0 {
					return (used / total) * 100.0
				}
			}
		}
	}
	return 0.0
}

func getNetworkBytes() (int64, int64) {
	cmd := exec.Command("cat", "/proc/net/dev")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	var totalIn, totalOut int64
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) >= 9 {
			bytesIn, err1 := strconv.ParseInt(fields[0], 10, 64)
			bytesOut, err2 := strconv.ParseInt(fields[8], 10, 64)
			if err1 == nil && err2 == nil {
				totalIn += bytesIn
				totalOut += bytesOut
			}
		}
	}

	return totalIn, totalOut
}

func GetInetIn() int {
	netMutex.Lock()
	defer netMutex.Unlock()

	currentIn, currentOut := getNetworkBytes()
	currentTime := time.Now().UnixMilli()

	if lastInetIn == 0 || lastNetTime == 0 {
		lastInetIn = currentIn
		lastInetOut = currentOut
		lastNetTime = currentTime
		return 0
	}

	deltaBytes := currentIn - lastInetIn
	deltaTime := currentTime - lastNetTime

	lastInetIn = currentIn
	lastInetOut = currentOut
	lastNetTime = currentTime

	if deltaBytes < 0 || deltaTime <= 0 {
		return 0
	}

	// Возвращаем байты/сек
	return int((deltaBytes * 1000) / deltaTime)
}

func GetInetOut() int {
	netMutex.Lock()
	defer netMutex.Unlock()

	currentIn, currentOut := getNetworkBytes()
	currentTime := time.Now().UnixMilli()

	if lastInetOut == 0 || lastNetTime == 0 {
		lastInetIn = currentIn
		lastInetOut = currentOut
		lastNetTime = currentTime
		return 0
	}

	deltaBytes := currentOut - lastInetOut
	deltaTime := currentTime - lastNetTime

	lastInetIn = currentIn
	lastInetOut = currentOut
	lastNetTime = currentTime

	if deltaBytes < 0 || deltaTime <= 0 {
		return 0
	}

	// Возвращаем байты/сек
	return int((deltaBytes * 1000) / deltaTime)
}
