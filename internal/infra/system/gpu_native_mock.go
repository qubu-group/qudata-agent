//go:build !linux || !cgo

package system

func nativeGPUCount() int {
	return 0
}

func nativeGPUName() string {
	return ""
}

func nativeVRAM() float64 {
	return 0.0
}

func nativeMaxCUDAVersion() float64 {
	return 12.2
}

func nativeGPUTemperature() int {
	return 0
}

func nativeGPUUtil() float64 {
	return 0.0
}

func nativeGPUMemoryUtil() float64 {
	return 0.0
}
