package qudata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/qudata/agent/internal/domain"
)

// Client communicates with the Qudata API for agent lifecycle and telemetry.
type Client struct {
	baseURL string
	apiKey  string

	mu     sync.RWMutex
	secret string

	http   *http.Client
	logger *slog.Logger
}

// NewClient creates a Qudata API client with the given API key and base URL.
func NewClient(apiKey, baseURL string, logger *slog.Logger) *Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 10 * time.Second
	retryClient.Logger = nil // suppress default logging

	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    retryClient.StandardClient(),
		logger:  logger,
	}
}

// UseSecret switches from API key auth to secret key auth.
func (c *Client) UseSecret(secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secret = secret
}

// Ping verifies connectivity to the Qudata API.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doRequest(ctx, http.MethodGet, "/ping", nil)
	return err
}

// InitAgent registers or re-initializes the agent with the API.
func (c *Client) InitAgent(ctx context.Context, req domain.InitAgentRequest) (*domain.InitAgentRespData, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal init request: %w", err)
	}

	data, err := c.doRequest(ctx, http.MethodPost, "/init", body)
	if err != nil {
		return nil, fmt.Errorf("init agent: %w", err)
	}

	var resp domain.InitAgentResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal init response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("init agent: API returned ok=false")
	}
	return &resp.Data, nil
}

// RegisterHost sends the host hardware configuration to the API.
func (c *Client) RegisterHost(ctx context.Context, req domain.CreateHostRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal host request: %w", err)
	}

	_, err = c.doRequest(ctx, http.MethodPost, "/init/host", body)
	if err != nil {
		return fmt.Errorf("register host: %w", err)
	}
	return nil
}

// SendStats publishes a telemetry snapshot to the API.
func (c *Client) SendStats(ctx context.Context, report domain.StatsReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	_, err = c.doRequest(ctx, http.MethodPost, "/stats", body)
	return err
}

// --- internal ---

func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	c.mu.RLock()
	secret := c.secret
	c.mu.RUnlock()

	if secret != "" {
		req.Header.Set("X-Agent-Secret", secret)
	} else {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.logger.Error("API error",
			"method", method,
			"path", path,
			"status", resp.StatusCode,
			"body", string(respBody),
		)
		return nil, fmt.Errorf("API %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
