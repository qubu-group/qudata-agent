package system

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

// StatsCollector реализует impls.StatsCollector.
type StatsCollector struct {
	netMu     sync.Mutex
	lastIn    int64
	lastOut   int64
	lastNetTs int64
}

func NewStatsCollector() *StatsCollector {
	return &StatsCollector{}
}

func (c *StatsCollector) Collect() domain.StatsSnapshot {
	inetIn, inetOut := c.networkSnapshot()
	return domain.StatsSnapshot{
		GPUUtil: gpuUtilSafe(),
		GPUTemp: gpuTemperatureSafe(),
		CPUUtil: cpuUtil(),
		RAMUtil: ramUtil(),
		MemUtil: gpuMemUtilSafe(),
		InetIn:  inetIn,
		InetOut: inetOut,
	}
}

func cpuUtil() float64 {
	cmd := exec.Command("sh", "-c", "top -bn1 | grep 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1}'")
	output, err := cmd.Output()
	if err != nil {
		logger.LogWarn("Get CPU Utilization: %v", err)
		return 0.0
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		logger.LogWarn("Get CPU Utilization: %v", err)
		return 0.0
	}
	return util
}

func ramUtil() float64 {
	cmd := exec.Command("free", "-b")
	output, err := cmd.Output()
	if err != nil {
		logger.LogWarn("Get RAM Utilization: %v", err)
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

func (c *StatsCollector) networkSnapshot() (int, int) {
	c.netMu.Lock()
	defer c.netMu.Unlock()
	currentIn, currentOut := readNetworkBytes()
	now := time.Now().UnixMilli()

	if c.lastNetTs == 0 {
		c.lastIn = currentIn
		c.lastOut = currentOut
		c.lastNetTs = now
		return 0, 0
	}

	deltaIn := currentIn - c.lastIn
	deltaOut := currentOut - c.lastOut
	deltaTime := now - c.lastNetTs

	c.lastIn = currentIn
	c.lastOut = currentOut
	c.lastNetTs = now

	if deltaTime <= 0 {
		return 0, 0
	}

	var inRate, outRate int
	if deltaIn > 0 {
		inRate = int((deltaIn * 1000) / deltaTime)
	}
	if deltaOut > 0 {
		outRate = int((deltaOut * 1000) / deltaTime)
	}
	return inRate, outRate
}

func readNetworkBytes() (int64, int64) {
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
