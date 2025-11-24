package httpserver

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
