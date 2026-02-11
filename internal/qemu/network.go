package qemu

import (
	"fmt"
	"strings"
)

// PortForward describes a single host-to-guest port forwarding rule.
type PortForward struct {
	Protocol  string // "tcp" or "udp"
	HostPort  int
	GuestPort int
}

// NetworkConfig builds QEMU user-mode networking arguments with port forwarding.
type NetworkConfig struct {
	id       string
	bindAddr string // "127.0.0.1" (default/FRPC) or "0.0.0.0" (test mode)
	forwards []PortForward
}

// NewNetworkConfig creates a network configuration.
// When testMode is true, ports are bound to 0.0.0.0 (publicly accessible).
func NewNetworkConfig(id string, testMode bool) *NetworkConfig {
	bind := "127.0.0.1"
	if testMode {
		bind = "0.0.0.0"
	}
	return &NetworkConfig{id: id, bindAddr: bind}
}

// AddForward registers a port forwarding rule from a host port to a guest port.
func (n *NetworkConfig) AddForward(proto string, hostPort, guestPort int) {
	n.forwards = append(n.forwards, PortForward{
		Protocol:  proto,
		HostPort:  hostPort,
		GuestPort: guestPort,
	})
}

// Args returns the QEMU command-line arguments for user-mode networking.
func (n *NetworkConfig) Args() []string {
	var fwds []string
	for _, f := range n.forwards {
		fwds = append(fwds, fmt.Sprintf(
			"hostfwd=%s:%s:%d-:%d",
			f.Protocol, n.bindAddr, f.HostPort, f.GuestPort,
		))
	}

	netdev := fmt.Sprintf("user,id=%s", n.id)
	if len(fwds) > 0 {
		netdev += "," + strings.Join(fwds, ",")
	}

	return []string{
		"-netdev", netdev,
		"-device", fmt.Sprintf("virtio-net-pci,netdev=%s", n.id),
	}
}
