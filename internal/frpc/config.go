package frpc

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/qudata/agent/internal/domain"
)

type Config struct {
	ServerAddr      string
	ServerPort      int
	AuthToken       string
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

{{- if .AgentProxy }}

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
