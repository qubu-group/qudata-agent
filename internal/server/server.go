package server

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/runtime"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"net/http"
	"strconv"
	"time"
)

type Server struct {
	runtime *runtime.Runtime
	server  *http.Server
}

func NewServer(runtime *runtime.Runtime) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", pingHandler)
	mux.HandleFunc("/instances", instancesHandler)
	mux.HandleFunc("/ssh", sshHandler)

	server := &http.Server{
		Addr:              "0.0.0.0:" + strconv.Itoa(runtime.AgentPort),
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return &Server{runtime: runtime, server: server}
}

func (s *Server) Run() {
	utils.LogInfo("server starting on %s", s.server.Addr)
	err := s.server.ListenAndServe()
	utils.LogError("server stopped: %v", err)
	panic(err)
}
