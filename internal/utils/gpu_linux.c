#include <stdio.h>
#include <stdlib.h>
#include <nvml.h>


int get_gpu_count() {
    nvmlReturn_t result;
    unsigned int count = 0;

    result = nvmlInit_v2();
    if (result != NVML_SUCCESS)
        return -1;

    result = nvmlDeviceGetCount_v2(&count);
    nvmlShutdown();

    if (result != NVML_SUCCESS)
        return -1;
    return (int)count;
}

int get_gpu_name(char *name, unsigned int length) {
    nvmlReturn_t result;
    nvmlDevice_t device;

    result = nvmlInit_v2();
    if (result != NVML_SUCCESS)
        return 0;

    result = nvmlDeviceGetHandleByIndex_v2(0, &device);
    if (result != NVML_SUCCESS) {
        nvmlShutdown();
        return 0;
    }

    result = nvmlDeviceGetName(device, name, length);
    nvmlShutdown();

    return (result == NVML_SUCCESS);
}

double get_gpu_vram() {
    nvmlReturn_t result;
    nvmlDevice_t device;
    nvmlMemory_t memory;

    result = nvmlInit_v2();
    if (result != NVML_SUCCESS)
        return -1.0;

    result = nvmlDeviceGetHandleByIndex_v2(0, &device);
    if (result != NVML_SUCCESS) {
        nvmlShutdown();
        return -1.0;
    }

    result = nvmlDeviceGetMemoryInfo(device, &memory);
    nvmlShutdown();

    if (result != NVML_SUCCESS)
        return -1.0;

    return (double)memory.total / (1024.0 * 1024.0 * 1024.0);
}

double get_max_cuda_version() {
    nvmlReturn_t result;
    int cudaVersion = 0;

    result = nvmlInit_v2();
    if (result != NVML_SUCCESS)
        return 0.0;

    result = nvmlSystemGetCudaDriverVersion(&cudaVersion);
    nvmlShutdown();

    if (result != NVML_SUCCESS)
        return 0.0;

    int major = cudaVersion / 1000;
    int minor = (cudaVersion % 1000) / 10;
    
    return (double)major + ((double)minor / 10.0);
}
