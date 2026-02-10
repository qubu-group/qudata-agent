package domain

// StatsSnapshot holds a point-in-time sample of system metrics.
type StatsSnapshot struct {
	GPUUtil float64 `json:"gpu_util"`
	GPUTemp int     `json:"gpu_temp"`
	CPUUtil float64 `json:"cpu_util"`
	RAMUtil float64 `json:"ram_util"`
	MemUtil float64 `json:"mem_util"`
	InetIn  uint64  `json:"inet_in"`
	InetOut uint64  `json:"inet_out"`
}

// StatsReport is the payload sent to the Qudata API.
type StatsReport struct {
	StatsSnapshot
	Status InstanceStatus `json:"status"`
}
