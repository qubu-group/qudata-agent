package domain

// InitAgentRequest is sent to the Qudata API during agent bootstrap.
type InitAgentRequest struct {
	AgentID     string `json:"agent_id"`
	AgentPort   int    `json:"agent_port"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	PID         int    `json:"pid"`
	Version     string `json:"version"`
}

// InitAgentResponse is returned by the Qudata API after agent init.
type InitAgentResponse struct {
	OK   bool              `json:"ok"`
	Data InitAgentRespData `json:"data"`
}

type InitAgentRespData struct {
	AgentCreated    bool     `json:"agent_created"`
	EmergencyReinit bool     `json:"emergency_reinit"`
	HostExists      bool     `json:"host_exists"`
	SecretKey       string   `json:"secret_key"`
	InstanceRunning bool     `json:"instance_running"`
	FRP             *FRPInfo `json:"frp,omitempty"`
}

// FRPInfo contains FRP server connection details returned by the API.
type FRPInfo struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	Token      string `json:"token"`
	Subdomain  string `json:"subdomain"`
}

// AgentMetadata holds runtime metadata for the running agent.
type AgentMetadata struct {
	ID          string
	Port        int
	Address     string
	Fingerprint string
	SecretKey   string
	FRP         *FRPInfo
}
