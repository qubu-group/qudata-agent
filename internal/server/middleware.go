package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware validates the X-Agent-Secret header against the expected secret.
func AuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// /ping is public and does not require auth
		if c.Request.URL.Path == "/ping" {
			c.Next()
			return
		}

		provided := c.GetHeader("X-Agent-Secret")
		if provided == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"ok":    false,
				"error": "missing X-Agent-Secret header",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"ok":    false,
				"error": "invalid secret",
			})
			return
		}

		c.Next()
	}
}

// LoggingMiddleware logs each request with duration and status.
func LoggingMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		logger.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration", time.Since(start).String(),
			"ip", c.ClientIP(),
		)
	}
}

// RecoveryMiddleware catches panics and returns a 500 error.
func RecoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					"error", r,
					"path", c.Request.URL.Path,
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"ok":    false,
					"error": "internal server error",
				})
			}
		}()
		c.Next()
	}
}
