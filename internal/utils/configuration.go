package utils

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type UnitValue struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

type ConfigurationData struct {
	RAM         UnitValue `json:"ram,omitempty"`
	Disk        UnitValue `json:"disk,omitempty"`
	CPUName     string    `json:"cpu_name,omitempty"`
	CPUCores    int       `json:"cpu_cores,omitempty"`
	CPUFreq     float64   `json:"cpu_freq,omitempty"`
	MemorySpeed float64   `json:"memory_speed,omitempty"`
	EthernetIn  float64   `json:"ethernet_in,omitempty"`
	EthernetOut float64   `json:"ethernet_out,omitempty"`
	Capacity    float64   `json:"capacity,omitempty"`
	MaxCUDAVer  float64   `json:"max_cuda_version,omitempty"`
}

func GetConfiguration() ConfigurationData {
	return ConfigurationData{
		RAM:         getRAM(),
		Disk:        getDisk(),
		CPUName:     getCPUName(),
		CPUCores:    getCPUCores(),
		CPUFreq:     getCPUFreq(),
		MemorySpeed: getMemorySpeed(),
		EthernetIn:  getNetworkSpeed(),
		EthernetOut: getNetworkSpeed(),
		Capacity:    getCapacity(),
		MaxCUDAVer:  GetMaxCUDAVersion(),
	}
}

func getRAM() UnitValue {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return UnitValue{Amount: 0, Unit: "gb"}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseFloat(fields[1], 64)
				gb := kb / 1024 / 1024
				return UnitValue{Amount: gb, Unit: "gb"}
			}
		}
	}
	return UnitValue{Amount: 0, Unit: "gb"}
}

func getDisk() UnitValue {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return UnitValue{Amount: 0, Unit: "gb"}
	}
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	gb := float64(totalBytes) / 1024 / 1024 / 1024
	return UnitValue{Amount: gb, Unit: "gb"}
}

func getCPUName() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func getCPUCores() int {
	return runtime.NumCPU()
}

func getCPUFreq() float64 {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 0.0
	}
	defer file.Close()

	var maxFreq float64
	var totalFreq float64
	var count int

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu MHz") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				mhz, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				if err == nil {
					ghz := mhz / 1000
					totalFreq += ghz
					count++
					if ghz > maxFreq {
						maxFreq = ghz
					}
				}
			}
		}
	}

	// Возвращаем максимальную частоту (boost frequency)
	if maxFreq > 0 {
		return maxFreq
	}

	// Если максимальной нет, возвращаем среднюю
	if count > 0 {
		return totalFreq / float64(count)
	}

	return 0.0
}

func getMemorySpeed() float64 {
	file, err := os.Open("/sys/devices/system/memory/memory0/state")
	if err != nil {
		return 2400.0
	}
	file.Close()
	return 2400.0
}

func getNetworkSpeed() float64 {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		LogWarn("Failed to read /sys/class/net")
		return 1.0
	}

	var maxSpeed float64

	for _, entry := range entries {
		iface := entry.Name()
		if !isInterfaceCandidate(iface) {
			continue
		}

		if !isInterfaceOperational(iface) {
			continue
		}

		speed := detectInterfaceSpeed(iface)
		if speed > maxSpeed {
			maxSpeed = speed
		}
	}

	if maxSpeed > 0 {
		return maxSpeed
	}

	return 1.0
}

func getCapacity() float64 {
	cpuCores := float64(getCPUCores())
	cpuFreq := getCPUFreq()
	ram := getRAM().Amount
	return (cpuCores * cpuFreq * ram) / 100
}

func detectInterfaceSpeed(iface string) float64 {
	if speed := readInterfaceSpeedFromSysfs(iface); speed > 0 {
		return speed
	}
	if speed := readInterfaceSpeedViaEthtool(iface); speed > 0 {
		return speed
	}
	if speed := readInterfaceSpeedViaNetworkctl(iface); speed > 0 {
		return speed
	}
	if speed := readInterfaceSpeedViaNmcli(iface); speed > 0 {
		return speed
	}
	return 0
}

func readInterfaceSpeedFromSysfs(iface string) float64 {
	speedPath := "/sys/class/net/" + iface + "/speed"
	data, err := os.ReadFile(speedPath)
	if err != nil {
		return 0
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0
	}

	if value == "-1" || strings.Contains(strings.ToLower(value), "unknown") {
		return 0
	}

	speed, err := strconv.ParseFloat(value, 64)
	if err != nil || speed <= 0 {
		return 0
	}
	return speed
}

func readInterfaceSpeedViaEthtool(iface string) float64 {
	binary, err := exec.LookPath("ethtool")
	if err != nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, iface).CombinedOutput()
	if err != nil && len(output) == 0 {
		return 0
	}

	speed := parseSpeedFromText(string(output))
	if speed <= 0 && err != nil {
		LogWarn("ethtool %s failed: %v", iface, err)
	}
	return speed
}

func readInterfaceSpeedViaNetworkctl(iface string) float64 {
	binary, err := exec.LookPath("networkctl")
	if err != nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "status", iface, "--no-pager").CombinedOutput()
	if err != nil && len(output) == 0 {
		return 0
	}

	return parseSpeedFromText(string(output))
}

func readInterfaceSpeedViaNmcli(iface string) float64 {
	binary, err := exec.LookPath("nmcli")
	if err != nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "device", "show", iface).CombinedOutput()
	if err != nil && len(output) == 0 {
		return 0
	}

	return parseSpeedFromText(string(output))
}

func parseSpeedFromText(text string) float64 {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if !strings.Contains(strings.ToLower(line), "speed") {
			continue
		}

		normalized := strings.ToLower(line)
		normalized = strings.ReplaceAll(normalized, ":", " ")
		normalized = strings.ReplaceAll(normalized, "=", " ")
		fields := strings.Fields(normalized)
		if len(fields) == 0 {
			continue
		}

		for i, field := range fields {
			if field != "speed" {
				continue
			}

			if i+1 >= len(fields) {
				break
			}

			value, unit := extractSpeedValue(fields[i+1:])
			if value > 0 {
				return convertSpeedToMbps(value, unit)
			}
			break
		}
	}
	return 0
}

func extractSpeedValue(fields []string) (float64, string) {
	if len(fields) == 0 {
		return 0, ""
	}

	valueStr := strings.Trim(fields[0], "()")
	valueStr = strings.TrimSuffix(valueStr, ",")
	valueStr = strings.TrimSuffix(valueStr, ";")

	// Handle variants like "3000mb/s"
	unit := ""
	for _, suffix := range []string{"gbit/s", "gb/s", "gigabit", "mb/s", "mib/s", "mibit/s", "kbit/s", "kb/s"} {
		if strings.HasSuffix(valueStr, suffix) {
			unit = suffix
			valueStr = strings.TrimSpace(strings.TrimSuffix(valueStr, suffix))
			break
		}
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil || value <= 0 {
		return 0, ""
	}

	if unit == "" && len(fields) > 1 {
		unit = fields[1]
	}

	return value, unit
}

func convertSpeedToMbps(value float64, unit string) float64 {
	unit = strings.ToLower(unit)
	switch {
	case strings.HasPrefix(unit, "g"):
		return value * 1000
	case strings.HasPrefix(unit, "m"):
		return value
	case strings.HasPrefix(unit, "k"):
		return value / 1000
	default:
		// No unit provided, assume Mbps
		return value
	}
}

func isInterfaceCandidate(iface string) bool {
	if iface == "" || iface == "lo" {
		return false
	}

	virtualPrefixes := []string{
		"docker", "veth", "br-", "virbr", "tap", "tun", "wg", "tailscale",
		"zt", "vmnet", "lo:", "vlan", "macvtap", "macvlan",
	}

	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(iface, prefix) {
			return false
		}
	}
	return true
}

func isInterfaceOperational(iface string) bool {
	operstatePath := "/sys/class/net/" + iface + "/operstate"
	operstate, err := os.ReadFile(operstatePath)
	if err != nil {
		return false
	}

	state := strings.TrimSpace(strings.ToLower(string(operstate)))
	if state == "up" {
		return true
	}

	if state == "unknown" {
		carrierPath := "/sys/class/net/" + iface + "/carrier"
		carrier, err := os.ReadFile(carrierPath)
		if err == nil && strings.TrimSpace(string(carrier)) == "1" {
			return true
		}
	}
	return false
}
