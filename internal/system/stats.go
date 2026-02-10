package system

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/gpu"
)

// StatsCollector samples GPU, CPU, RAM, and network metrics.
// CPU utilization is computed between successive Collect() calls
// without any internal sleep — the caller controls the sampling interval.
type StatsCollector struct {
	gpu *gpu.Metrics

	mu           sync.Mutex
	prevInetIn   uint64
	prevInetOut  uint64
	prevCPUIdle  uint64
	prevCPUTotal uint64
	prevTime     time.Time
}

// NewStatsCollector creates a stats collector using the given GPU metrics.
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

// Collect returns a point-in-time snapshot of system metrics.
// CPU utilization is the delta since the last Collect() call.
// This method does NOT block.
func (c *StatsCollector) Collect() domain.StatsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	// CPU delta
	idle, total := cpuTimes()
	idleDelta := float64(idle - c.prevCPUIdle)
	totalDelta := float64(total - c.prevCPUTotal)
	cpuPercent := 0.0
	if totalDelta > 0 {
		cpuPercent = (1.0 - idleDelta/totalDelta) * 100.0
	}
	c.prevCPUIdle = idle
	c.prevCPUTotal = total

	// Network delta
	curIn, curOut := netCounters()
	deltaIn := curIn - c.prevInetIn
	deltaOut := curOut - c.prevInetOut
	c.prevInetIn = curIn
	c.prevInetOut = curOut
	c.prevTime = time.Now()

	return domain.StatsSnapshot{
		GPUUtil: c.gpu.Utilization(),
		GPUTemp: c.gpu.Temperature(),
		CPUUtil: cpuPercent,
		RAMUtil: ramUtil(),
		MemUtil: c.gpu.MemoryUtilization(),
		InetIn:  deltaIn,
		InetOut: deltaOut,
	}
}

// --- CPU ---

func cpuTimes() (idle, total uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0
	}
	// First line: "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
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
	idle = values[3] // idle
	if len(fields) > 5 {
		idle += values[4] // iowait
	}
	return idle, total
}

// --- RAM ---

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

// --- Network ---

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
