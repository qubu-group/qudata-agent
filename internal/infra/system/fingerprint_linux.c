#include <stdlib.h>
#include <string.h>
#include <nvml.h>

const char* getGpuSerial() {
    if (nvmlInit_v2() != NVML_SUCCESS)
        return NULL;
    nvmlDevice_t device;
    if (nvmlDeviceGetHandleByIndex_v2(0, &device) != NVML_SUCCESS) {
        nvmlShutdown();
        return NULL;
    }
    static char serial[128];
    if (nvmlDeviceGetSerial(device, serial, sizeof(serial)) != NVML_SUCCESS) {
        nvmlShutdown();
        return NULL;
    }
    nvmlShutdown();
    return serial;
}