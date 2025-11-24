package tunnel

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/state"
)

// Manager отвечает за управление туннельными портами и их восстановление.
type Manager struct {
	logger *logger.FileLogger

	mu        sync.Mutex
	state     *state.InstanceState
	listeners map[string]net.Listener // host port -> listener
	container string                  // cached container ip
}

func NewManager(log *logger.FileLogger) *Manager {
	return &Manager{
		logger:    log,
		listeners: make(map[string]net.Listener),
	}
}

// Configure настраивает туннель для текущего инстанса.
func (m *Manager) Configure(ctx context.Context, containerID, token string, ports domain.InstancePorts) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.stopLocked(); err != nil {
		return err
	}

	if token == "" || len(ports) == 0 {
		m.state = nil
		_ = state.ClearInstanceState()
		return nil
	}

	m.state = &state.InstanceState{
		ContainerID: containerID,
		Ports:       ports,
		TunnelToken: token,
	}

	if err := state.SaveInstanceState(m.state); err != nil {
		return err
	}

	return m.startLocked(ctx)
}

// Restore запускает слушателей из сохранённого состояния (если есть).
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	saved, err := state.LoadInstanceState()
	if err != nil {
		return err
	}
	if saved == nil || saved.ContainerID == "" || saved.TunnelToken == "" || len(saved.Ports) == 0 {
		return nil
	}

	m.state = saved
	return m.startLocked(ctx)
}

// Clear останавливает все туннели и очищает сохранённое состояние.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stopLocked(); err != nil {
		return err
	}
	m.state = nil
	m.container = ""
	return state.ClearInstanceState()
}

func (m *Manager) startLocked(ctx context.Context) error {
	if m.state == nil {
		return nil
	}
	if len(m.state.Ports) == 0 {
		return nil
	}
	hostToContainer := invertPorts(m.state.Ports)
	containerIP, err := containerIP(m.state.ContainerID)
	if err != nil {
		return err
	}
	m.container = containerIP

	for hostPort, containerPort := range hostToContainer {
		l, err := net.Listen("tcp", ":"+hostPort)
		if err != nil {
			m.logger.Warn("tunnel listen %s failed: %v", hostPort, err)
			continue
		}
		m.listeners[hostPort] = l
		go func(listener net.Listener) {
			<-ctx.Done()
			_ = listener.Close()
		}(l)
		go m.acceptLoop(ctx, l, hostPort, containerPort)
		m.logger.Info("tunnel listening on %s -> %s:%s", hostPort, containerIP, containerPort)
	}
	return nil
}

func (m *Manager) stopLocked() error {
	for port, l := range m.listeners {
		_ = l.Close()
		delete(m.listeners, port)
	}
	return nil
}

func (m *Manager) acceptLoop(ctx context.Context, ln net.Listener, hostPort, containerPort string) {
	done := ctx.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			select {
			case <-done:
			default:
			}
			return
		}
		go m.handleConn(conn, containerPort)
	}
}

func (m *Manager) handleConn(client net.Conn, containerPort string) {
	defer client.Close()

	m.mu.Lock()
	var token string
	if m.state != nil {
		token = m.state.TunnelToken
	}
	containerIP := m.container
	m.mu.Unlock()

	if token == "" || containerIP == "" {
		return
	}

	client.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(client)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	if subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Tunnel")), []byte(token)) != 1 {
		writeError(client, http.StatusForbidden, "Forbidden")
		return
	}
	req.Header.Del("X-Tunnel")

	backendAddr := fmt.Sprintf("%s:%s", containerIP, containerPort)
	backend, err := net.Dial("tcp", backendAddr)
	if err != nil {
		writeError(client, http.StatusBadGateway, "Backend unavailable")
		return
	}
	defer backend.Close()

	client.SetDeadline(time.Time{})
	backend.SetDeadline(time.Time{})

	if err := req.Write(backend); err != nil {
		return
	}

	io.Copy(client, backend)
}

func writeError(conn net.Conn, code int, msg string) {
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain\r\n\r\n%s",
		code, http.StatusText(code), len(msg), msg)
}

func invertPorts(ports map[string]string) map[string]string {
	hostToContainer := make(map[string]string)
	for container, host := range ports {
		if container == "22" || container == "22/tcp" {
			continue
		}
		hostToContainer[host] = strings.TrimSuffix(container, "/tcp")
	}
	return hostToContainer
}

func containerIP(containerID string) (string, error) {
	cmd := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return "", fmt.Errorf("container %s has no IP", containerID)
	}
	return ip, nil
}
