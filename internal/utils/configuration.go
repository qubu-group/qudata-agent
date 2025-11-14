package utils

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
		return 1.0
	}

	var maxSpeed float64 = 0

	for _, entry := range entries {
		ifaceName := entry.Name()

		if ifaceName == "lo" ||
			strings.HasPrefix(ifaceName, "docker") ||
			strings.HasPrefix(ifaceName, "veth") ||
			strings.HasPrefix(ifaceName, "br-") ||
			strings.HasPrefix(ifaceName, "virbr") {
			continue
		}

		operstatePath := "/sys/class/net/" + ifaceName + "/operstate"
		operstate, err := os.ReadFile(operstatePath)
		if err != nil || strings.TrimSpace(string(operstate)) != "up" {
			continue
		}

		speedPath := "/sys/class/net/" + ifaceName + "/speed"
		data, err := os.ReadFile(speedPath)
		if err != nil {
			continue
		}

		speed, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err != nil || speed <= 0 {
			continue
		}

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
