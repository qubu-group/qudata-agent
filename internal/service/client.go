package service

import (
	"bytes"
	"encoding/json"
	"github.com/magicaleks/qudata-agent-alpha/internal/models"
	"github.com/magicaleks/qudata-agent-alpha/internal/storage"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
)

const (
	BasePath        = "https://internal.qudata.ai/v0"
	ApiKeyHeader    = "X-API-Key"
	SecretKeyHeader = "X-Agent-Secret"
)

const (
	ApplicationJsonType = "application/json"
)

type Client struct {
	apiKey    string // Qudata API key
	secretKey string // Agent secret key
	http      *retryablehttp.Client
}

type response[T any] struct {
	Ok   bool `json:"ok"`
	Data *T   `json:"data"`
}

func newResponse[T any](body io.ReadCloser) *response[T] {
	defer body.Close()

	var resp response[T]
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		utils.LogWarn("failed to decode response: %v", err)
		return &response[T]{Ok: false}
	}
	return &resp
}

func NewServiceClient() *Client {
	apiKey := os.Getenv("QUDATA_API_KEY")
	if apiKey == "" && !strings.HasPrefix(apiKey, "ak-") {
		panic("invalid api key")
	}
	return &Client{
		apiKey:    apiKey,
		http:      retryablehttp.NewClient(),
		secretKey: storage.GetSecretKey(),
	}
}

func (c *Client) SetSecret(secretKey string) {
	if secretKey == "" && !strings.HasPrefix(secretKey, "sk-") {
		panic("invalid secret key")
	}
	c.secretKey = secretKey
	c.apiKey = ""
}

func (c *Client) do(method, path string, body any) (*http.Response, error) {
	var buf io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewBuffer(jsonData)
	}
	req, err := retryablehttp.NewRequest(method, BasePath+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ApplicationJsonType)
	if c.apiKey != "" {
		req.Header.Set(ApiKeyHeader, c.apiKey)
	}
	if c.secretKey != "" {
		req.Header.Set(SecretKeyHeader, c.secretKey)
	}
	return c.http.Do(req)
}

func (c *Client) Ping() bool {
	resp, err := c.do("GET", "/ping", nil)
	if err != nil {
		return false
	}
	data := newResponse[models.EmptyResponse](resp.Body)
	return data.Ok
}

func (c *Client) Init(request *models.InitAgentRequest) *models.InitAgentResponse {
	resp, err := c.do("POST", "/init", request)
	if err != nil {
		utils.LogError("failed to init agent: %v", err)
		return nil
	}
	data := newResponse[models.InitAgentResponse](resp.Body)
	return data.Data
}

func (c *Client) CreateHost(request *models.CreateHostRequest) {
	_, err := c.do("POST", "/init/host", request)
	if err != nil {
		utils.LogWarn("failed to create host: %v", err)
	}
}

func (c *Client) Stats(request *models.StatsRequest) {
	_, err := c.do("POST", "/stats", request)
	if err != nil {
		utils.LogWarn("failed to send stats: %v", err)
	}
}
