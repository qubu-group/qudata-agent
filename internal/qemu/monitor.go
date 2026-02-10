package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// QMPClient communicates with a QEMU instance via the QEMU Machine Protocol
// over a Unix domain socket. It handles the capabilities handshake, command
// execution, and asynchronous event filtering.
type QMPClient struct {
	socketPath string

	mu         sync.Mutex
	conn       net.Conn
	dec        *json.Decoder
	autoReconn bool // Enable automatic reconnection on failure
}

// qmpMessage is a union type that can represent any QMP response or event.
type qmpMessage struct {
	QMP       json.RawMessage `json:"QMP,omitempty"`
	Return    json.RawMessage `json:"return,omitempty"`
	Error     *qmpError       `json:"error,omitempty"`
	Event     string          `json:"event,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp json.RawMessage `json:"timestamp,omitempty"`
}

type qmpError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

type qmpCommand struct {
	Execute   string      `json:"execute"`
	Arguments interface{} `json:"arguments,omitempty"`
}

type qmpStatusReturn struct {
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

// NewQMPClient creates a QMP client targeting the given Unix socket path.
func NewQMPClient(socketPath string) *QMPClient {
	return &QMPClient{
		socketPath: socketPath,
		autoReconn: true,
	}
}

// SetAutoReconnect enables or disables automatic reconnection on failure.
func (c *QMPClient) SetAutoReconnect(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.autoReconn = enabled
}

// Connect dials the QMP socket and performs the mandatory capabilities handshake.
func (c *QMPClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked()
}

// connectLocked performs the actual connection. Must be called with c.mu held.
func (c *QMPClient) connectLocked() error {
	// Close existing connection if any
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.dec = nil
	}

	conn, err := net.DialTimeout("unix", c.socketPath, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.socketPath, err)
	}
	c.conn = conn
	c.dec = json.NewDecoder(conn)

	// Read the server greeting.
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var greeting qmpMessage
	if err := c.dec.Decode(&greeting); err != nil {
		conn.Close()
		c.conn = nil
		c.dec = nil
		return fmt.Errorf("read greeting: %w", err)
	}

	// Negotiate capabilities (required before any command).
	if _, err := c.exec("qmp_capabilities", nil); err != nil {
		conn.Close()
		c.conn = nil
		c.dec = nil
		return fmt.Errorf("negotiate capabilities: %w", err)
	}

	_ = conn.SetReadDeadline(time.Time{})
	return nil
}

// Reconnect attempts to re-establish the QMP connection.
func (c *QMPClient) Reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked()
}

// ensureConnected checks the connection and reconnects if necessary.
// Must be called with c.mu held.
func (c *QMPClient) ensureConnected() error {
	if c.conn != nil {
		return nil
	}
	if !c.autoReconn {
		return fmt.Errorf("qmp: not connected")
	}
	return c.connectLocked()
}

// Close terminates the QMP connection.
func (c *QMPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Connected reports whether the QMP socket is currently open.
func (c *QMPClient) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Shutdown sends an ACPI power-down request, triggering a graceful guest shutdown.
func (c *QMPClient) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.exec("system_powerdown", nil)
	return err
}

// Reset performs an immediate hardware reset of the guest.
func (c *QMPClient) Reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.exec("system_reset", nil)
	return err
}

// Pause halts guest CPU execution.
func (c *QMPClient) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.exec("stop", nil)
	return err
}

// Resume continues guest CPU execution after a Pause.
func (c *QMPClient) Resume() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.exec("cont", nil)
	return err
}

// Quit terminates the QEMU process immediately without guest shutdown.
func (c *QMPClient) Quit() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.exec("quit", nil)
	return err
}

// QueryStatus returns the current VM run state (e.g. "running", "paused").
func (c *QMPClient) QueryStatus() (status string, running bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	raw, err := c.exec("query-status", nil)
	if err != nil {
		return "", false, err
	}

	var result qmpStatusReturn
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", false, fmt.Errorf("unmarshal status: %w", err)
	}
	return result.Status, result.Running, nil
}

// exec sends a QMP command and returns the response payload.
// Asynchronous events received between the command and its response are silently skipped.
// Must be called with c.mu held.
func (c *QMPClient) exec(command string, args interface{}) (json.RawMessage, error) {
	// Try to ensure we're connected
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}

	result, err := c.execOnce(command, args)
	if err != nil {
		// If the command failed and auto-reconnect is enabled, try once more
		if c.autoReconn && (isConnectionError(err) || c.conn == nil) {
			if reconnErr := c.connectLocked(); reconnErr == nil {
				return c.execOnce(command, args)
			}
		}
		return nil, err
	}
	return result, nil
}

// execOnce sends a command without retry logic.
func (c *QMPClient) execOnce(command string, args interface{}) (json.RawMessage, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("qmp: not connected")
	}

	cmd := qmpCommand{Execute: command, Arguments: args}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal %q: %w", command, err)
	}

	if _, err := c.conn.Write(append(data, '\n')); err != nil {
		c.conn.Close()
		c.conn = nil
		c.dec = nil
		return nil, fmt.Errorf("write %q: %w", command, err)
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer func() {
		if c.conn != nil {
			_ = c.conn.SetReadDeadline(time.Time{})
		}
	}()

	for {
		var msg qmpMessage
		if err := c.dec.Decode(&msg); err != nil {
			c.conn.Close()
			c.conn = nil
			c.dec = nil
			return nil, fmt.Errorf("read response for %q: %w", command, err)
		}

		// Skip asynchronous events (SHUTDOWN, RESET, etc.).
		if msg.Event != "" {
			continue
		}

		if msg.Error != nil {
			return nil, fmt.Errorf("qmp %s: %s (%s)", command, msg.Error.Desc, msg.Error.Class)
		}

		return msg.Return, nil
	}
}

// isConnectionError checks if an error indicates a connection problem.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "not connected")
}
