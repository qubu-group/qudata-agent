package domain

type InitAgentRequest struct {
	AgentID     string `json:"agent_id"`
	AgentPort   int    `json:"agent_port"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	PID         int    `json:"pid"`
	Version     string `json:"version"`
}

type InitAgentResponse struct {
	OK   bool              `json:"ok"`
	Data InitAgentRespData `json:"data"`
}

type InitAgentRespData struct {
	AgentCreated    bool   `json:"agent_created"`
	EmergencyReinit bool   `json:"emergency_reinit"`
	HostExists      bool   `json:"host_exists"`
	SecretKey       string `json:"secret_key"`
	TunnelToken     string `json:"tunnel_token"`
	InstanceRunning bool   `json:"instance_running"`
}

type AgentMetadata struct {
	ID          string
	Port        int
	Address     string
	SecretKey   string
	TunnelToken string
	HostExists  bool
}
