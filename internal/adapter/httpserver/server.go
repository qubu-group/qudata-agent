package httpserver

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

type Server struct {
	http *http.Server
}

func NewServer(port int, api *API, secret string, logger impls.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), requestLogger(logger), authMiddleware(secret))
	router.Use(gin.CustomRecovery(requestRecoveryWithLog(logger)))
	api.RegisterRoutes(router)

	s := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		Handler:           router,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &Server{http: s}
}

func (s *Server) Run() error {
	return s.http.ListenAndServe()
}
