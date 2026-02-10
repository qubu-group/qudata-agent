package domain

// CreateHostRequest is sent to register the host hardware with the Qudata API.
type CreateHostRequest struct {
	GPUName       string       `json:"gpu_name"`
	GPUAmount     int          `json:"gpu_amount"`
	VRAM          float64      `json:"vram"`
	MaxCUDA       float64      `json:"max_cuda"`
	Location      HostLocation `json:"location"`
	Configuration HostConfig   `json:"configuration"`
}

// HostLocation describes the geographic location of the host.
type HostLocation struct {
	City    string `json:"city"`
	Country string `json:"country"`
	Region  string `json:"region"`
}

// HostConfig describes the hardware configuration of the host.
type HostConfig struct {
	RAM            ResourceUnit `json:"ram"`
	Disk           ResourceUnit `json:"disk"`
	CPUName        string       `json:"cpu_name"`
	CPUCores       int          `json:"cpu_cores"`
	CPUFreq        float64      `json:"cpu_freq"`
	MemorySpeed    float64      `json:"memory_speed"`
	EthernetIn     float64      `json:"ethernet_in"`
	EthernetOut    float64      `json:"ethernet_out"`
	Capacity       float64      `json:"capacity"`
	MaxCUDAVersion float64      `json:"max_cuda_version"`
}

// ResourceUnit is a value with a unit label (e.g. 64.0 "gb").
type ResourceUnit struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}
