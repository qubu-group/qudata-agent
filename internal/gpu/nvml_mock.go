//go:build !linux || !cgo

package gpu

func nvmlAvailable() bool           { return false }
func nativeGPUCount() int           { return 1 }
func nativeGPUName() string         { return "Mock" }
func nativeVRAM() float64           { return 0 }
func nativeMaxCUDAVersion() float64 { return 0 }
func nativeGPUTemperature() int     { return 0 }
func nativeGPUUtil() float64        { return 0 }
func nativeGPUMemoryUtil() float64  { return 0 }
func nativeFingerprint() string     { return "mock-fingerprint" }
