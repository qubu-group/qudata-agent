package domain

// AgentMetadata описывает основные параметры агента, известные приложению.
type AgentMetadata struct {
	ID          string
	Port        int
	Address     string
	Fingerprint string
	PID         int
}

// InitAgentRequest используется для инициализации агента на стороне Qudata.
type InitAgentRequest struct {
	AgentID     string `json:"agent_id"`
	AgentPort   int    `json:"agent_port"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	PID         int    `json:"pid"`
}

// InitAgentResponse отражает состояние агента на стороне сервиса.
type InitAgentResponse struct {
	AgentCreated    bool   `json:"agent_created"`
	EmergencyReinit bool   `json:"emergency_reinit"`
	HostExists      bool   `json:"host_exists"`
	SecretKey       string `json:"secret_key,omitempty"`
	InstanceRunning bool   `json:"instance_running"`
}
