/*
 * NVML dynamic loading via dlopen.
 *
 * The binary does NOT link against libnvidia-ml at compile time.
 * Instead, it loads the library at runtime using dlopen.
 * If the library is not present (no GPU / no driver), all functions
 * return safe error values and the agent continues in debug mode.
 */

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ── Minimal NVML type definitions (avoids nvml.h header dependency) ── */

typedef int nvmlReturn_t;
#define NVML_SUCCESS 0

typedef struct { void *_; } *nvmlDevice_t;

typedef struct {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;

typedef struct {
    unsigned int gpu;
    unsigned int memory;
} nvmlUtilization_t;

#define NVML_TEMPERATURE_GPU 0

/* ── Function pointer types ── */

typedef nvmlReturn_t (*fn_nvmlInit)(void);
typedef nvmlReturn_t (*fn_nvmlShutdown)(void);
typedef nvmlReturn_t (*fn_nvmlDeviceGetCount)(unsigned int *);
typedef nvmlReturn_t (*fn_nvmlDeviceGetHandleByIndex)(unsigned int, nvmlDevice_t *);
typedef nvmlReturn_t (*fn_nvmlDeviceGetName)(nvmlDevice_t, char *, unsigned int);
typedef nvmlReturn_t (*fn_nvmlDeviceGetMemoryInfo)(nvmlDevice_t, nvmlMemory_t *);
typedef nvmlReturn_t (*fn_nvmlSystemGetCudaDriverVersion)(int *);
typedef nvmlReturn_t (*fn_nvmlDeviceGetTemperature)(nvmlDevice_t, int, unsigned int *);
typedef nvmlReturn_t (*fn_nvmlDeviceGetUtilizationRates)(nvmlDevice_t, nvmlUtilization_t *);
typedef nvmlReturn_t (*fn_nvmlDeviceGetSerial)(nvmlDevice_t, char *, unsigned int);

/* ── Global state ── */

static void *nvml_handle = NULL;
static int   nvml_available = -1; /* -1 = not checked, 0 = no, 1 = yes */

static fn_nvmlInit                      p_Init;
static fn_nvmlShutdown                  p_Shutdown;
static fn_nvmlDeviceGetCount            p_GetCount;
static fn_nvmlDeviceGetHandleByIndex    p_GetHandle;
static fn_nvmlDeviceGetName             p_GetName;
static fn_nvmlDeviceGetMemoryInfo       p_GetMemInfo;
static fn_nvmlSystemGetCudaDriverVersion p_GetCudaVer;
static fn_nvmlDeviceGetTemperature      p_GetTemp;
static fn_nvmlDeviceGetUtilizationRates p_GetUtil;
static fn_nvmlDeviceGetSerial           p_GetSerial;

/* ── Library loader ── */

static int nvml_load(void) {
    if (nvml_available >= 0)
        return nvml_available;

    /* Try loading the runtime library (from NVIDIA driver) */
    nvml_handle = dlopen("libnvidia-ml.so.1", RTLD_LAZY);
    if (!nvml_handle)
        nvml_handle = dlopen("libnvidia-ml.so", RTLD_LAZY);
    if (!nvml_handle) {
        nvml_available = 0;
        return 0;
    }

    /* Resolve symbols */
    p_Init       = (fn_nvmlInit)dlsym(nvml_handle, "nvmlInit_v2");
    p_Shutdown   = (fn_nvmlShutdown)dlsym(nvml_handle, "nvmlShutdown");
    p_GetCount   = (fn_nvmlDeviceGetCount)dlsym(nvml_handle, "nvmlDeviceGetCount_v2");
    p_GetHandle  = (fn_nvmlDeviceGetHandleByIndex)dlsym(nvml_handle, "nvmlDeviceGetHandleByIndex_v2");
    p_GetName    = (fn_nvmlDeviceGetName)dlsym(nvml_handle, "nvmlDeviceGetName");
    p_GetMemInfo = (fn_nvmlDeviceGetMemoryInfo)dlsym(nvml_handle, "nvmlDeviceGetMemoryInfo");
    p_GetCudaVer = (fn_nvmlSystemGetCudaDriverVersion)dlsym(nvml_handle, "nvmlSystemGetCudaDriverVersion");
    p_GetTemp    = (fn_nvmlDeviceGetTemperature)dlsym(nvml_handle, "nvmlDeviceGetTemperature");
    p_GetUtil    = (fn_nvmlDeviceGetUtilizationRates)dlsym(nvml_handle, "nvmlDeviceGetUtilizationRates");
    p_GetSerial  = (fn_nvmlDeviceGetSerial)dlsym(nvml_handle, "nvmlDeviceGetSerial");

    /* Verify critical symbols are present */
    if (!p_Init || !p_Shutdown || !p_GetCount || !p_GetHandle) {
        dlclose(nvml_handle);
        nvml_handle = NULL;
        nvml_available = 0;
        return 0;
    }

    nvml_available = 1;
    return 1;
}

/* ── Public API (called from Go via CGO) ── */

int gpu_is_available(void) {
    return nvml_load();
}

int gpu_get_count(void) {
    if (!nvml_load()) return -1;
    unsigned int count = 0;
    if (p_Init() != NVML_SUCCESS) return -1;
    nvmlReturn_t r = p_GetCount(&count);
    p_Shutdown();
    return (r == NVML_SUCCESS) ? (int)count : -1;
}

int gpu_get_name(char *name, unsigned int length) {
    if (!nvml_load()) return 0;
    nvmlDevice_t dev;
    if (p_Init() != NVML_SUCCESS) return 0;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return 0; }
    nvmlReturn_t r = p_GetName(dev, name, length);
    p_Shutdown();
    return (r == NVML_SUCCESS);
}

double gpu_get_vram(void) {
    if (!nvml_load() || !p_GetMemInfo) return -1.0;
    nvmlDevice_t dev;
    nvmlMemory_t mem;
    if (p_Init() != NVML_SUCCESS) return -1.0;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return -1.0; }
    nvmlReturn_t r = p_GetMemInfo(dev, &mem);
    p_Shutdown();
    return (r == NVML_SUCCESS) ? (double)mem.total / (1024.0*1024.0*1024.0) : -1.0;
}

double gpu_get_max_cuda_version(void) {
    if (!nvml_load() || !p_GetCudaVer) return 0.0;
    int ver = 0;
    if (p_Init() != NVML_SUCCESS) return 0.0;
    nvmlReturn_t r = p_GetCudaVer(&ver);
    p_Shutdown();
    if (r != NVML_SUCCESS) return 0.0;
    return (double)(ver/1000) + (double)((ver%1000)/10) / 10.0;
}

int gpu_get_temperature(void) {
    if (!nvml_load() || !p_GetTemp) return -1;
    nvmlDevice_t dev;
    unsigned int temp = 0;
    if (p_Init() != NVML_SUCCESS) return -1;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return -1; }
    nvmlReturn_t r = p_GetTemp(dev, NVML_TEMPERATURE_GPU, &temp);
    p_Shutdown();
    return (r == NVML_SUCCESS) ? (int)temp : -1;
}

int gpu_get_utilization(void) {
    if (!nvml_load() || !p_GetUtil) return -1;
    nvmlDevice_t dev;
    nvmlUtilization_t util;
    if (p_Init() != NVML_SUCCESS) return -1;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return -1; }
    nvmlReturn_t r = p_GetUtil(dev, &util);
    p_Shutdown();
    return (r == NVML_SUCCESS) ? (int)util.gpu : -1;
}

int gpu_get_memory_utilization(void) {
    if (!nvml_load() || !p_GetUtil) return -1;
    nvmlDevice_t dev;
    nvmlUtilization_t util;
    if (p_Init() != NVML_SUCCESS) return -1;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return -1; }
    nvmlReturn_t r = p_GetUtil(dev, &util);
    p_Shutdown();
    return (r == NVML_SUCCESS) ? (int)util.memory : -1;
}

/* ── Fingerprint (GPU serial) ── */

const char* gpu_get_serial(void) {
    if (!nvml_load() || !p_GetSerial) return NULL;
    nvmlDevice_t dev;
    if (p_Init() != NVML_SUCCESS) return NULL;
    if (p_GetHandle(0, &dev) != NVML_SUCCESS) { p_Shutdown(); return NULL; }
    static char serial[128];
    nvmlReturn_t r = p_GetSerial(dev, serial, sizeof(serial));
    p_Shutdown();
    return (r == NVML_SUCCESS) ? serial : NULL;
}
