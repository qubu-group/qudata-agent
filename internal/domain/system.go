package domain

// UnitValue описывает значение с единицей измерения.
type UnitValue struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// ConfigurationData содержит основные параметры железа.
type ConfigurationData struct {
	RAM         UnitValue `json:"ram,omitempty"`
	Disk        UnitValue `json:"disk,omitempty"`
	CPUName     string    `json:"cpu_name,omitempty"`
	CPUCores    int       `json:"cpu_cores,omitempty"`
	CPUFreq     float64   `json:"cpu_freq,omitempty"`
	MemorySpeed float64   `json:"memory_speed,omitempty"`
	EthernetIn  float64   `json:"ethernet_in,omitempty"`
	EthernetOut float64   `json:"ethernet_out,omitempty"`
	Capacity    float64   `json:"capacity,omitempty"`
	MaxCUDAVer  float64   `json:"max_cuda_version,omitempty"`
}
