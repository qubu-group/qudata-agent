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
// User-mode networking requires no host privileges and is fully compatible
// with the existing FRPC tunnel scheme (ports bound on 127.0.0.1).
type NetworkConfig struct {
	id       string
	forwards []PortForward
}

// NewNetworkConfig creates a network configuration with the given netdev identifier.
func NewNetworkConfig(id string) *NetworkConfig {
	return &NetworkConfig{id: id}
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
// Example output:
//
//	-netdev user,id=net0,hostfwd=tcp:127.0.0.1:45001-:22,hostfwd=tcp:127.0.0.1:45002-:8888
//	-device virtio-net-pci,netdev=net0
func (n *NetworkConfig) Args() []string {
	var fwds []string
	for _, f := range n.forwards {
		fwds = append(fwds, fmt.Sprintf(
			"hostfwd=%s:127.0.0.1:%d-:%d",
			f.Protocol, f.HostPort, f.GuestPort,
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
