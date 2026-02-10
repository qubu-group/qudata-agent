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
	AgentCreated    bool     `json:"agent_created"`
	EmergencyReinit bool     `json:"emergency_reinit"`
	HostExists      bool     `json:"host_exists"`
	SecretKey       string   `json:"secret_key"`
	InstanceRunning bool     `json:"instance_running"`
	FRP             *FRPInfo `json:"frp,omitempty"`
}

type FRPInfo struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	Token      string `json:"token"`
	Subdomain  string `json:"subdomain"`
}

type AgentMetadata struct {
	ID         string
	Port       int
	Address    string
	SecretKey  string
	FRP        *FRPInfo
	HostExists bool
}

func (m *AgentMetadata) Subdomain() string {
	if m != nil && m.FRP != nil {
		return m.FRP.Subdomain
	}
	return ""
}
