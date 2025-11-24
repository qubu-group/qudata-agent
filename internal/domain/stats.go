package domain

// StatsSnapshot содержит срез телеметрии, отправляемый в Qudata.
type StatsSnapshot struct {
	GPUUtil float64        `json:"gpu_util"`
	GPUTemp int            `json:"gpu_temp"`
	CPUUtil float64        `json:"cpu_util"`
	RAMUtil float64        `json:"ram_util"`
	MemUtil float64        `json:"mem_util"`
	InetIn  int            `json:"inet_in"`
	InetOut int            `json:"inet_out"`
	Status  InstanceStatus `json:"status,omitempty"`
}
