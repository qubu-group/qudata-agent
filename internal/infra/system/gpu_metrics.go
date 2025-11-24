package system

import "os"

func debugMode() bool {
	return os.Getenv("QUDATA_AGENT_DEBUG") == "true"
}

func gpuCountSafe() int {
	if debugMode() {
		return 1
	}
	return nativeGPUCount()
}

func gpuNameSafe() string {
	if debugMode() {
		return "H100"
	}
	return nativeGPUName()
}

func gpuVRAMSafe() float64 {
	if debugMode() {
		return 70.0
	}
	return nativeVRAM()
}

func gpuMaxCUDASafe() float64 {
	if debugMode() {
		return 12.2
	}
	return nativeMaxCUDAVersion()
}

func gpuTemperatureSafe() int {
	if debugMode() {
		return 45
	}
	return nativeGPUTemperature()
}

func gpuUtilSafe() float64 {
	if debugMode() {
		return 0.0
	}
	return nativeGPUUtil()
}

func gpuMemUtilSafe() float64 {
	if debugMode() {
		return 0.0
	}
	return nativeGPUMemoryUtil()
}
