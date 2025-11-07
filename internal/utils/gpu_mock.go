//go:build !linux || !cgo

package utils

import "fmt"

func GetGPUCount() int {
	fmt.Println("[Mock] GPU count unavailable")
	return 0
}

func GetGPUName() string {
	fmt.Println("[Mock] GPU (no NVML)")
	return ""
}

func GetVRAM() float64 {
	return 0.0
}

func GetMaxCUDAVersion() float64 {
	return 12.2
}
