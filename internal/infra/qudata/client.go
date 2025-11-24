package qudata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
)

const (
	basePath        = "https://internal.qudata.ai/v0"
	apiKeyHeader    = "X-API-Key"
	secretKeyHeader = "X-Agent-Secret"
	applicationJSON = "application/json"
)

type Client struct {
	apiKey    string
	secretKey string
	http      *retryablehttp.Client
}

func NewClient(secret string) *Client {
	apiKey := os.Getenv("QUDATA_API_KEY")
	if secret == "" {
		if apiKey == "" || !strings.HasPrefix(apiKey, "ak-") {
			panic("QUDATA_API_KEY is not defined or invalid")
		}
	}

	return &Client{
		apiKey:    apiKey,
		secretKey: secret,
		http:      retryablehttp.NewClient(),
	}
}

func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/ping", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data := decodeResponse[struct{}](resp.Body)
	if !data.Ok {
		return errors.New("qudata ping response is not ok")
	}
	return nil
}

func (c *Client) InitAgent(ctx context.Context, req domain.InitAgentRequest) (*domain.InitAgentResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/init", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data := decodeResponse[domain.InitAgentResponse](resp.Body)
	if !data.Ok || data.Data == nil {
		return nil, errors.New("empty init response")
	}
	return data.Data, nil
}

func (c *Client) RegisterHost(ctx context.Context, req domain.CreateHostRequest) error {
	resp, err := c.do(ctx, http.MethodPost, "/init/host", req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) SendStats(ctx context.Context, stats domain.StatsSnapshot) error {
	resp, err := c.do(ctx, http.MethodPost, "/stats", stats)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) UseSecret(secret string) {
	if secret == "" || !strings.HasPrefix(secret, "sk-") {
		panic("invalid secret key")
	}
	c.secretKey = secret
	c.apiKey = ""
}

type apiResponse[T any] struct {
	Ok   bool `json:"ok"`
	Data *T   `json:"data"`
}

func decodeResponse[T any](body io.Reader) apiResponse[T] {
	var resp apiResponse[T]
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return apiResponse[T]{Ok: false}
	}
	return resp
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		payload = bytes.NewReader(data)
	}

	req, err := retryablehttp.NewRequest(method, basePath+path, payload)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", applicationJSON)

	if c.apiKey != "" {
		req.Header.Set(apiKeyHeader, c.apiKey)
	}
	if c.secretKey != "" {
		req.Header.Set(secretKeyHeader, c.secretKey)
	}
	return c.http.Do(req)
}
