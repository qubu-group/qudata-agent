package frpc

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/qudata/agent/internal/domain"
)

// Config represents a complete FRPC configuration.
type Config struct {
	ServerAddr string
	ServerPort int
	AuthToken  string

	// AgentProxy is the proxy for the agent's HTTP API endpoint.
	AgentProxy *Proxy

	// InstanceProxies are proxies for VM instance ports.
	InstanceProxies []Proxy
}

// Proxy describes a single FRPC proxy entry.
type Proxy struct {
	Name         string
	Type         string // "tcp" or "http"
	LocalIP      string
	LocalPort    int
	RemotePort   int    // for TCP proxies
	CustomDomain string // for HTTP proxies
}

// configTemplate is the TOML template for frpc.toml generation.
var configTemplate = template.Must(template.New("frpc").Parse(`serverAddr = "{{ .ServerAddr }}"
serverPort = {{ .ServerPort }}

[auth]
method = "token"
token = "{{ .AuthToken }}"

{{- if .AgentProxy }}

# Agent API endpoint
[[proxies]]
name = "{{ .AgentProxy.Name }}"
type = "{{ .AgentProxy.Type }}"
{{- if eq .AgentProxy.Type "http" }}
customDomains = ["{{ .AgentProxy.CustomDomain }}"]
{{- end }}
localIP = "{{ .AgentProxy.LocalIP }}"
localPort = {{ .AgentProxy.LocalPort }}
{{- if and (eq .AgentProxy.Type "tcp") (gt .AgentProxy.RemotePort 0) }}
remotePort = {{ .AgentProxy.RemotePort }}
{{- end }}
{{- end }}

{{- range .InstanceProxies }}

# Instance proxy: {{ .Name }}
[[proxies]]
name = "{{ .Name }}"
type = "{{ .Type }}"
{{- if eq .Type "http" }}
customDomains = ["{{ .CustomDomain }}"]
{{- end }}
localIP = "{{ .LocalIP }}"
localPort = {{ .LocalPort }}
{{- if and (eq .Type "tcp") (gt .RemotePort 0) }}
remotePort = {{ .RemotePort }}
{{- end }}
{{- end }}
`))

// NewConfig creates a base FRPC config from FRP connection info and the agent's
// local HTTP port. The subdomain is used as the HTTP customDomain for the agent.
func NewConfig(frp *domain.FRPInfo, agentPort int) *Config {
	return &Config{
		ServerAddr: frp.ServerAddr,
		ServerPort: frp.ServerPort,
		AuthToken:  frp.Token,
		AgentProxy: &Proxy{
			Name:         "agent-api",
			Type:         "http",
			LocalIP:      "127.0.0.1",
			LocalPort:    agentPort,
			CustomDomain: frp.Subdomain,
		},
	}
}

// AddInstanceProxies appends FRP proxies for a newly created VM instance.
func (c *Config) AddInstanceProxies(proxies []domain.FRPProxy) {
	for _, p := range proxies {
		c.InstanceProxies = append(c.InstanceProxies, Proxy{
			Name:         p.Name,
			Type:         p.Type,
			LocalIP:      "127.0.0.1",
			LocalPort:    p.LocalPort,
			RemotePort:   p.RemotePort,
			CustomDomain: p.CustomDomain,
		})
	}
}

// ClearInstanceProxies removes all instance proxies (used on instance deletion).
func (c *Config) ClearInstanceProxies() {
	c.InstanceProxies = nil
}

// Render generates the TOML config file content.
func (c *Config) Render() ([]byte, error) {
	var buf bytes.Buffer
	if err := configTemplate.Execute(&buf, c); err != nil {
		return nil, fmt.Errorf("render frpc config: %w", err)
	}
	return buf.Bytes(), nil
}

// BuildInstanceProxies creates FRP proxy definitions from the instance spec
// and the allocated host ports. The subdomain is used for HTTP proxy customDomains.
func BuildInstanceProxies(spec domain.InstanceSpec, hostPorts []int, subdomain string) []domain.FRPProxy {
	proxies := make([]domain.FRPProxy, 0, len(spec.Ports))
	for i, pm := range spec.Ports {
		if i >= len(hostPorts) {
			break
		}
		hostPort := hostPorts[i]

		proxy := domain.FRPProxy{
			Name:      fmt.Sprintf("vm-%s-%d", pm.Proto, pm.ContainerPort),
			Type:      pm.Proto,
			LocalPort: hostPort,
		}

		switch pm.Proto {
		case "tcp":
			proxy.RemotePort = pm.RemotePort
		case "http":
			if pm.RemotePort > 0 {
				proxy.CustomDomain = fmt.Sprintf("%s:%d", subdomain, pm.RemotePort)
			} else {
				proxy.CustomDomain = subdomain
			}
		}

		proxies = append(proxies, proxy)
	}
	return proxies
}
