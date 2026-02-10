package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/gpu"
)

// StatsCollector samples GPU, CPU, RAM, and network metrics.
// GPU metrics can come from either host NVML or VM via SSH.
type StatsCollector struct {
	gpu       *gpu.Metrics
	vmMetrics domain.VMGPUMetricsProvider

	mu           sync.Mutex
	prevInetIn   uint64
	prevInetOut  uint64
	prevCPUIdle  uint64
	prevCPUTotal uint64
	prevTime     time.Time

	lastVMMetrics *domain.VMGPUMetrics
	lastVMUpdate  time.Time
}

func NewStatsCollector(gpuMetrics *gpu.Metrics) *StatsCollector {
	idle, total := cpuTimes()
	inetIn, inetOut := netCounters()
	return &StatsCollector{
		gpu:          gpuMetrics,
		prevInetIn:   inetIn,
		prevInetOut:  inetOut,
		prevCPUIdle:  idle,
		prevCPUTotal: total,
		prevTime:     time.Now(),
	}
}

func (c *StatsCollector) SetVMMetricsProvider(provider domain.VMGPUMetricsProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vmMetrics = provider
}

func (c *StatsCollector) Collect() domain.StatsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	idle, total := cpuTimes()
	idleDelta := float64(idle - c.prevCPUIdle)
	totalDelta := float64(total - c.prevCPUTotal)
	cpuPercent := 0.0
	if totalDelta > 0 {
		cpuPercent = (1.0 - idleDelta/totalDelta) * 100.0
	}
	c.prevCPUIdle = idle
	c.prevCPUTotal = total

	curIn, curOut := netCounters()
	deltaIn := curIn - c.prevInetIn
	deltaOut := curOut - c.prevInetOut
	c.prevInetIn = curIn
	c.prevInetOut = curOut
	c.prevTime = time.Now()

	gpuUtil, gpuTemp, memUtil := c.collectGPUMetrics()

	return domain.StatsSnapshot{
		GPUUtil: gpuUtil,
		GPUTemp: gpuTemp,
		CPUUtil: cpuPercent,
		RAMUtil: ramUtil(),
		MemUtil: memUtil,
		InetIn:  deltaIn,
		InetOut: deltaOut,
	}
}

func (c *StatsCollector) collectGPUMetrics() (util float64, temp int, memUtil float64) {
	if c.vmMetrics != nil && c.vmMetrics.SSHReady() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		metrics, err := c.vmMetrics.GetGPUMetrics(ctx)
		if err == nil && metrics != nil {
			c.lastVMMetrics = metrics
			c.lastVMUpdate = time.Now()
			return metrics.Utilization, metrics.Temperature, metrics.MemoryUtilization()
		}

		if c.lastVMMetrics != nil && time.Since(c.lastVMUpdate) < 5*time.Second {
			return c.lastVMMetrics.Utilization, c.lastVMMetrics.Temperature, c.lastVMMetrics.MemoryUtilization()
		}
	}

	return c.gpu.Utilization(), c.gpu.Temperature(), c.gpu.MemoryUtilization()
}

func cpuTimes() (idle, total uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 {
		return 0, 0
	}
	var values [10]uint64
	for i := 1; i < len(fields) && i <= 10; i++ {
		fmt.Sscanf(fields[i], "%d", &values[i-1])
	}
	for _, v := range values {
		total += v
	}
	idle = values[3]
	if len(fields) > 5 {
		idle += values[4]
	}
	return idle, total
}

func ramUtil() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var memTotal, memAvailable uint64
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			fmt.Sscanf(line, "MemTotal: %d kB", &memTotal)
		case strings.HasPrefix(line, "MemAvailable:"):
			fmt.Sscanf(line, "MemAvailable: %d kB", &memAvailable)
		}
	}
	if memTotal == 0 {
		return 0
	}
	return float64(memTotal-memAvailable) / float64(memTotal) * 100.0
}

func netCounters() (rxBytes, txBytes uint64) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") || strings.HasPrefix(line, "Inter") || strings.HasPrefix(line, "face") {
			continue
		}
		if strings.HasPrefix(line, "lo:") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}
		var rx, tx uint64
		fmt.Sscanf(fields[0], "%d", &rx)
		fmt.Sscanf(fields[8], "%d", &tx)
		rxBytes += rx
		txBytes += tx
	}
	return rxBytes, txBytes
}
