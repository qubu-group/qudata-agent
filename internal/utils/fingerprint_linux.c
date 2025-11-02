#include <stdlib.h>
#include <string.h>
#include <nvml.h>

const char* getGpuName() {
    if (nvmlInit_v2() != NVML_SUCCESS)
        return NULL;
    nvmlDevice_t device;
    if (nvmlDeviceGetHandleByIndex_v2(0, &device) != NVML_SUCCESS) {
        nvmlShutdown();
        return NULL;
    }
    static char name[128];
    if (nvmlDeviceGetName(device, name, sizeof(name)) != NVML_SUCCESS) {
        nvmlShutdown();
        return NULL;
    }
    nvmlShutdown();
    return name;
}