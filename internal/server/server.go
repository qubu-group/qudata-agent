package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/frpc"
	"github.com/qudata/agent/internal/network"
	"github.com/qudata/agent/internal/storage"
)

type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

func New(
	port int,
	secret string,
	subdomain string,
	vm domain.VMManager,
	frpcProc *frpc.Process,
	ports *network.PortAllocator,
	store *storage.Store,
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	router.Use(RecoveryMiddleware(logger))
	router.Use(LoggingMiddleware(logger))
	router.Use(AuthMiddleware(secret))

	h := NewHandler(vm, frpcProc, ports, store, logger)

	router.GET("/ping", h.Ping)
	router.GET("/instances", h.GetInstance)
	router.POST("/instances", h.CreateInstance)
	router.PUT("/instances", h.ManageInstance)
	router.DELETE("/instances", h.DeleteInstance)
	router.POST("/ssh", h.AddSSH)
	router.DELETE("/ssh", h.RemoveSSH)

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf("127.0.0.1:%d", port),
			Handler:      router,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		logger: logger,
	}
}

func (s *Server) Start() error {
	s.logger.Info("HTTP server starting", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("HTTP server shutting down")
	return s.httpServer.Shutdown(ctx)
}
