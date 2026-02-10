//go:build linux && cgo

package gpu

/*
#cgo LDFLAGS: -ldl

int gpu_is_available(void);
int gpu_get_count(void);
int gpu_get_name(char *name, unsigned int length);
double gpu_get_vram(void);
double gpu_get_max_cuda_version(void);
int gpu_get_temperature(void);
int gpu_get_utilization(void);
int gpu_get_memory_utilization(void);
const char* gpu_get_serial(void);
*/
import "C"
import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"strings"
)

func nvmlAvailable() bool {
	return C.gpu_is_available() == 1
}

func nativeGPUCount() int {
	return int(math.Max(float64(C.gpu_get_count()), 1))
}

func nativeGPUName() string {
	var name [128]C.char
	if C.gpu_get_name(&name[0], C.uint(len(name))) == 0 {
		return ""
	}
	return formatGPUName(C.GoString(&name[0]))
}

func nativeVRAM() float64 {
	vram := C.gpu_get_vram()
	if vram < 0 {
		return 0.0
	}
	return float64(vram)
}

func nativeMaxCUDAVersion() float64 {
	v := C.gpu_get_max_cuda_version()
	if v <= 0 {
		return 0.0
	}
	return float64(v)
}

func nativeGPUTemperature() int {
	t := C.gpu_get_temperature()
	if t < 0 {
		return 0
	}
	return int(t)
}

func nativeGPUUtil() float64 {
	u := C.gpu_get_utilization()
	if u < 0 {
		return 0.0
	}
	return float64(u)
}

func nativeGPUMemoryUtil() float64 {
	u := C.gpu_get_memory_utilization()
	if u < 0 {
		return 0.0
	}
	return float64(u)
}

// formatGPUName strips common prefixes/suffixes from NVIDIA GPU names.
func formatGPUName(fullName string) string {
	result := fullName
	for _, prefix := range []string{"NVIDIA ", "GeForce ", "Tesla "} {
		result = strings.ReplaceAll(result, prefix, "")
	}
	result = strings.ReplaceAll(result, " Ti", "Ti")
	result = strings.ReplaceAll(result, " ", "")
	return result
}

// nativeFingerprint generates a unique machine fingerprint using
// GPU serial + /etc/machine-id.
func nativeFingerprint() string {
	var parts []string

	serial := C.gpu_get_serial()
	if serial != nil {
		parts = append(parts, C.GoString(serial))
	}

	machineID, err := os.ReadFile("/etc/machine-id")
	if err == nil {
		parts = append(parts, strings.TrimSpace(string(machineID)))
	}

	if len(parts) == 0 {
		hostname, _ := os.Hostname()
		parts = append(parts, hostname)
	}

	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
