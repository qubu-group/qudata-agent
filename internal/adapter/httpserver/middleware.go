package httpserver

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

func authMiddleware(secret string) gin.HandlerFunc {
	expected := strings.TrimSpace(secret)
	if expected == "" {
		expected = "agent_secret"
	}

	return func(c *gin.Context) {
		if subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Agent-Secret")), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, response{
				Ok:    false,
				Error: "unauthorized",
			})
			return
		}
		c.Next()
	}
}

func requestLogger(logger impls.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		status := c.Writer.Status()
		latency := time.Since(start)
		logger.Info("%s %s -> %d (%s)", method, path, status, latency)
	}
}

func requestRecoveryWithLog(logger impls.Logger) gin.RecoveryFunc {
	return func(c *gin.Context, err any) {
		var body []byte
		if c.Request.Body != nil {
			b, _ := io.ReadAll(c.Request.Body)
			body = b
			c.Request.Body = io.NopCloser(bytes.NewBuffer(b))
		}

		logger.Error(fmt.Sprintf(
			"[ERR] %v | %s %s | hdr=%v | body=%q | stack=%s",
			err,
			c.Request.Method,
			c.Request.URL.String(),
			c.Request.Header,
			string(body),
			debug.Stack(),
		))
	}
}
