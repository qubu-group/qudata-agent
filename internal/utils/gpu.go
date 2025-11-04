//go:build linux && cgo

package utils

/*
#cgo LDFLAGS: -lnvidia-ml

int get_gpu_count();
int get_gpu_name(char *name, unsigned int length);
double get_gpu_vram();
double get_max_cuda_version();
*/
import "C"
import "math"

func GetGPUCount() int {
	return int(math.Max(float64(C.get_gpu_count()), 1))
}

func GetGPUName() string {
	var name [128]C.char
	if C.get_gpu_name(&name[0], C.uint(len(name))) == 0 {
		return ""
	}
	return C.GoString(&name[0])
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
