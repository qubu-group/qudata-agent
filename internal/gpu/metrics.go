package gpu

import "log/slog"

// Metrics provides a safe wrapper around NVML GPU functions.
// If NVML is unavailable (no driver) or debug mode is enabled,
// all methods return mock values. The binary starts and works in either case.
type Metrics struct {
	debug   bool
	hasNVML bool
	logger  *slog.Logger
}

// NewMetrics creates a GPU metrics provider.
// Automatically probes for NVML availability via dlopen.
func NewMetrics(debug bool, logger *slog.Logger) *Metrics {
	m := &Metrics{
		debug:  debug,
		logger: logger,
	}

	if !debug {
		m.hasNVML = nvmlAvailable()
		if m.hasNVML {
			logger.Info("NVML loaded successfully via dlopen")
		} else {
			logger.Warn("NVML not available — GPU metrics will return mock values")
		}
	} else {
		logger.Info("debug mode — using mock GPU data")
	}

	return m
}

// Available returns true if real GPU metrics are accessible.
func (m *Metrics) Available() bool {
	return m.hasNVML && !m.debug
}

// Count returns the number of GPUs.
func (m *Metrics) Count() int {
	if !m.Available() {
		return 1
	}
	return nativeGPUCount()
}

// Name returns the formatted GPU model name.
func (m *Metrics) Name() string {
	if !m.Available() {
		return "H100"
	}
	return nativeGPUName()
}

// VRAM returns total GPU memory in GiB.
func (m *Metrics) VRAM() float64 {
	if !m.Available() {
		return 70.0
	}
	return nativeVRAM()
}

// MaxCUDAVersion returns the maximum supported CUDA version.
func (m *Metrics) MaxCUDAVersion() float64 {
	if !m.Available() {
		return 12.2
	}
	return nativeMaxCUDAVersion()
}

// Temperature returns GPU temperature in degrees Celsius.
func (m *Metrics) Temperature() int {
	if !m.Available() {
		return 45
	}
	return nativeGPUTemperature()
}

// Utilization returns GPU compute utilization (0-100).
func (m *Metrics) Utilization() float64 {
	if !m.Available() {
		return 0.0
	}
	return nativeGPUUtil()
}

// MemoryUtilization returns GPU memory utilization (0-100).
func (m *Metrics) MemoryUtilization() float64 {
	if !m.Available() {
		return 0.0
	}
	return nativeGPUMemoryUtil()
}

// GetFingerprint returns the machine fingerprint.
func (m *Metrics) GetFingerprint() string {
	if m.debug {
		return "debug-fingerprint-mock"
	}
	return nativeFingerprint()
}
