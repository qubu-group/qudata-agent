//go:build linux && cgo

package utils

/*
#cgo LDFLAGS: -lnvidia-ml

int get_gpu_count();
int get_gpu_name(char *name, unsigned int length);
double get_gpu_vram();
double get_max_cuda_version();
int get_gpu_temperature();
int get_gpu_utilization();
int get_gpu_memory_utilization();
*/
import "C"
import (
	"math"
	"strings"
)

func GetGPUCount() int {
	return int(math.Max(float64(C.get_gpu_count()), 1))
}

func GetGPUName() string {
	var name [128]C.char
	if C.get_gpu_name(&name[0], C.uint(len(name))) == 0 {
		return ""
	}
	fullName := C.GoString(&name[0])
	return formatGPUName(fullName)
}

func formatGPUName(fullName string) string {
	result := fullName
	result = strings.ReplaceAll(result, "NVIDIA ", "")
	result = strings.ReplaceAll(result, "GeForce ", "")
	result = strings.ReplaceAll(result, "Tesla ", "")
	result = strings.ReplaceAll(result, " Ti", "Ti")
	result = strings.ReplaceAll(result, " ", "")
	return result
}

func GetVRAM() float64 {
	vram := C.get_gpu_vram()
	if vram < 0 {
		return 0.0
	}
	return float64(vram)
}

func GetMaxCUDAVersion() float64 {
	version := C.get_max_cuda_version()
	if version <= 0 {
		return 0.0
	}
	return float64(version)
}

func GetGPUTemperature() int {
	temp := C.get_gpu_temperature()
	if temp < 0 {
		return 0
	}
	return int(temp)
}

func GetGPUUtil() float64 {
	util := C.get_gpu_utilization()
	if util < 0 {
		return 0.0
	}
	return float64(util)
}

func GetMemUtil() float64 {
	util := C.get_gpu_memory_utilization()
	if util < 0 {
		return 0.0
	}
	return float64(util)
}
