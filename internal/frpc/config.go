package frpc

import (
	"bytes"
	"fmt"
	"text/template"
)

const (
	FRPServerAddr = "agent.ru1.qudata.ai"
	FRPServerPort = 7000
	FRPToken      = "dd9915115e84def1436eade2687a66b7c0f1f54715eb16d2985745e0f4d0af79"
	DomainSuffix  = ".ru1.qudata.ai"
)

type Config struct {
	ServerAddr string
	ServerPort int
	AuthToken  string

	AgentProxy      *Proxy
	InstanceProxies []Proxy
}

type Proxy struct {
	Name         string
	Type         string
	LocalIP      string
	LocalPort    int
	RemotePort   int
	CustomDomain string
}

var configTemplate = template.Must(template.New("frpc").Parse(`serverAddr = "{{ .ServerAddr }}"
serverPort = {{ .ServerPort }}

[auth]
method = "token"
token = "{{ .AuthToken }}"

[transport]
tcpMuxKeepaliveInterval = 30
heartbeatInterval = -1
dialServerKeepalive = 60

[log]
to = "console"
level = "debug"

{{- if .AgentProxy }}

[[proxies]]
name = "{{ .AgentProxy.Name }}"
type = "{{ .AgentProxy.Type }}"
{{- if .AgentProxy.CustomDomain }}
customDomains = ["{{ .AgentProxy.CustomDomain }}"]
{{- end }}
localIP = "{{ .AgentProxy.LocalIP }}"
localPort = {{ .AgentProxy.LocalPort }}
{{- end }}

{{- range .InstanceProxies }}

[[proxies]]
name = "{{ .Name }}"
type = "{{ .Type }}"
{{- if .CustomDomain }}
customDomains = ["{{ .CustomDomain }}"]
{{- end }}
localIP = "{{ .LocalIP }}"
localPort = {{ .LocalPort }}
{{- if and (eq .Type "tcp") (gt .RemotePort 0) }}
remotePort = {{ .RemotePort }}
{{- end }}
{{- end }}
`))

func NewConfig(agentID, tunnelToken string, agentPort int) *Config {
	return &Config{
		ServerAddr: FRPServerAddr,
		ServerPort: FRPServerPort,
		AuthToken:  FRPToken,
		AgentProxy: &Proxy{
			Name:         fmt.Sprintf("agent-%s", agentID),
			Type:         "http",
			LocalIP:      "127.0.0.1",
			LocalPort:    agentPort,
			CustomDomain: fmt.Sprintf("%s-%d", tunnelToken, agentPort),
		},
	}
}

func (c *Config) AddInstanceProxy(p Proxy) {
	c.InstanceProxies = append(c.InstanceProxies, p)
}

func (c *Config) ClearInstanceProxies() {
	c.InstanceProxies = nil
}

func (c *Config) Render() ([]byte, error) {
	var buf bytes.Buffer
	if err := configTemplate.Execute(&buf, c); err != nil {
		return nil, fmt.Errorf("render frpc config: %w", err)
	}
	return buf.Bytes(), nil
}

func BuildInstanceProxies(tunnelToken string, hostPorts []int, sshRemotePort int, sshEnabled bool, ports []PortSpec) []Proxy {
	var proxies []Proxy
	idx := 0

	if sshEnabled && idx < len(hostPorts) {
		proxies = append(proxies, Proxy{
			Name:       "vm-ssh",
			Type:       "tcp",
			LocalIP:    "127.0.0.1",
			LocalPort:  hostPorts[idx],
			RemotePort: sshRemotePort,
		})
		idx++
	}

	for _, ps := range ports {
		if idx >= len(hostPorts) {
			break
		}
		proxy := Proxy{
			Name:      fmt.Sprintf("vm-%s-%d", ps.Proto, ps.GuestPort),
			Type:      ps.Proto,
			LocalIP:   "127.0.0.1",
			LocalPort: hostPorts[idx],
		}
		switch ps.Proto {
		case "tcp":
			proxy.RemotePort = ps.RemotePort
		case "http":
			if ps.RemotePort > 0 {
				proxy.CustomDomain = fmt.Sprintf("%s-%d", tunnelToken, ps.RemotePort)
			} else {
				proxy.CustomDomain = tunnelToken
			}
		}
		proxies = append(proxies, proxy)
		idx++
	}

	return proxies
}

type PortSpec struct {
	GuestPort  int
	RemotePort int
	Proto      string
}
