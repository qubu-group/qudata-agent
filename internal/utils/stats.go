package utils

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

var (
	lastInetIn  int
	lastInetOut int
	netMutex    sync.Mutex
)

func GetGPUUtil() float64 {
	cmd := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return 0.0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0.0
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
	if err != nil {
		return 0.0
	}
	return util
}

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
	cmd := exec.Command("sh", "-c", "free | grep Mem | awk '{print ($3/$2) * 100.0}'")
	output, err := cmd.Output()
	if err != nil {
		LogWarn("Get RAM Utilization: %s", err.Error())
		return 0.0
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		LogWarn("Get RAM Utilization: %s", err.Error())
		return 0.0
	}
	return util
}

func GetMemUtil() float64 {
	cmd := exec.Command("nvidia-smi", "--query-gpu=utilization.memory", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return 0.0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0.0
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
	if err != nil {
		return 0.0
	}
	return util
}

func getNetworkBytes() (int, int) {
	cmdIn := exec.Command("sh", "-c", "cat /proc/net/dev | grep -v lo | awk 'NR>2 {sum+=$2} END {print sum}'")
	outputIn, err := cmdIn.Output()
	if err != nil {
		return 0, 0
	}

	bytesIn, err := strconv.Atoi(strings.TrimSpace(string(outputIn)))
	if err != nil {
		bytesIn = 0
	}

	cmdOut := exec.Command("sh", "-c", "cat /proc/net/dev | grep -v lo | awk 'NR>2 {sum+=$10} END {print sum}'")
	outputOut, err := cmdOut.Output()
	if err != nil {
		return bytesIn, 0
	}

	bytesOut, err := strconv.Atoi(strings.TrimSpace(string(outputOut)))
	if err != nil {
		bytesOut = 0
	}

	return bytesIn, bytesOut
}

func GetInetIn() int {
	netMutex.Lock()
	defer netMutex.Unlock()

	currentIn, _ := getNetworkBytes()

	if lastInetIn == 0 {
		lastInetIn = currentIn
		return 0
	}

	delta := currentIn - lastInetIn
	lastInetIn = currentIn

	if delta < 0 {
		return 0
	}
	return delta
}

func GetInetOut() int {
	netMutex.Lock()
	defer netMutex.Unlock()

	_, currentOut := getNetworkBytes()

	if lastInetOut == 0 {
		lastInetOut = currentOut
		return 0
	}

	delta := currentOut - lastInetOut
	lastInetOut = currentOut

	if delta < 0 {
		return 0
	}
	return delta
}
