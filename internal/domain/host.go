package domain

// Location задает географические данные узла.
type Location struct {
	City    string `json:"city,omitempty"`
	Country string `json:"country,omitempty"`
	Region  string `json:"region,omitempty"`
}

// CreateHostRequest описывает параметры создаваемого хоста.
type CreateHostRequest struct {
	GPUName       string            `json:"gpu_name"`
	GPUAmount     int               `json:"gpu_amount"`
	VRAM          float64           `json:"vram"`
	MaxCUDA       float64           `json:"max_cuda"`
	Location      Location          `json:"location,omitempty"`
	Configuration ConfigurationData `json:"configuration"`
}
