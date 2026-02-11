package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/frpc"
	"github.com/qudata/agent/internal/network"
	"github.com/qudata/agent/internal/storage"
)

// TODO: hardcoded ports — replace with dynamic config from API when ready.
const ollamaGuestPort = 11434

type Handler struct {
	vm     domain.VMManager
	frpc   *frpc.Process
	ports  *network.PortAllocator
	store  *storage.Store
	logger *slog.Logger
}

func NewHandler(
	vm domain.VMManager,
	frpc *frpc.Process,
	ports *network.PortAllocator,
	store *storage.Store,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		vm:     vm,
		frpc:   frpc,
		ports:  ports,
		store:  store,
		logger: logger,
	}
}

func (h *Handler) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type createInstanceRequest struct {
	TunnelToken string `json:"tunnel_token" binding:"required"`
	DiskSizeGB  int    `json:"disk_size_gb"`
	CPUs        string `json:"cpus"`
	Memory      string `json:"memory"`
}

func (h *Handler) CreateInstance(c *gin.Context) {
	var req createInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// SSH: guest 22 → TCP remotePort from 10000-15000
	sshRemotePort, err := h.ports.AllocateSSHPort()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "ssh port: " + err.Error()})
		return
	}

	sshHostPort, err := h.ports.AllocateAppPorts(1)
	if err != nil {
		h.ports.Release(sshRemotePort)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "ssh local port: " + err.Error()})
		return
	}

	// Ollama: guest 11434 → HTTP on app port
	ollamaHostPort, err := h.ports.AllocateAppPorts(1)
	if err != nil {
		h.ports.Release(sshRemotePort)
		h.ports.Release(sshHostPort...)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "ollama port: " + err.Error()})
		return
	}

	// Ollama remote port for customDomains = ["token:<port>"]
	ollamaRemotePort, err := h.ports.AllocateAppPorts(1)
	if err != nil {
		h.ports.Release(sshRemotePort)
		h.ports.Release(sshHostPort...)
		h.ports.Release(ollamaHostPort...)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "ollama remote port: " + err.Error()})
		return
	}

	spec := domain.InstanceSpec{
		SSHEnabled:  true,
		TunnelToken: req.TunnelToken,
		DiskSizeGB:  req.DiskSizeGB,
		CPUs:        req.CPUs,
		Memory:      req.Memory,
		Ports: []domain.PortMapping{
			{Name: "ollama", GuestPort: ollamaGuestPort, RemotePort: ollamaRemotePort[0], Proto: "http"},
		},
	}

	// hostPorts order: [sshHost, ollamaHost] — matches BuildInstanceProxies expectations.
	hostPorts := []int{sshHostPort[0], ollamaHostPort[0]}

	go h.startInstance(context.Background(), spec, hostPorts, sshRemotePort)

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"data": gin.H{
			"ports": gin.H{
				"22":    strconv.Itoa(sshRemotePort),
				"11434": strconv.Itoa(ollamaRemotePort[0]),
			},
		},
	})
}

func (h *Handler) startInstance(ctx context.Context, spec domain.InstanceSpec, hostPorts []int, sshRemotePort int) {
	portMap, err := h.vm.Create(ctx, spec, hostPorts)
	if err != nil {
		h.logger.Error("failed to create instance", "err", err)
		return
	}

	var portSpecs []frpc.PortSpec
	for _, pm := range spec.Ports {
		portSpecs = append(portSpecs, frpc.PortSpec{
			GuestPort:  pm.GuestPort,
			RemotePort: pm.RemotePort,
			Proto:      pm.Proto,
		})
	}

	proxies := frpc.BuildInstanceProxies(spec.TunnelToken, hostPorts, sshRemotePort, spec.SSHEnabled, portSpecs)
	if err := h.frpc.UpdateInstanceProxies(proxies); err != nil {
		h.logger.Error("failed to update frpc proxies", "err", err)
	}

	state := &domain.InstanceState{
		VMID:        h.vm.VMID(),
		Ports:       portMap,
		SSHEnabled:  spec.SSHEnabled,
		GPUAddr:     spec.GPUAddr,
		TunnelToken: spec.TunnelToken,
	}
	if err := h.store.SaveInstanceState(state); err != nil {
		h.logger.Error("failed to save instance state", "err", err)
	}

	h.logger.Info("instance created", "vm_id", h.vm.VMID(), "ports", portMap)
}

func (h *Handler) GetInstance(c *gin.Context) {
	status := h.vm.Status(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"data": gin.H{"status": string(status)},
	})
}

type manageInstanceRequest struct {
	Command string `json:"command" binding:"required"`
}

func (h *Handler) ManageInstance(c *gin.Context) {
	var req manageInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	cmd := domain.InstanceCommand(req.Command)
	if err := h.vm.Manage(c.Request.Context(), cmd); err != nil {
		code := http.StatusInternalServerError
		if _, ok := err.(domain.ErrNoInstanceRunning); ok {
			code = http.StatusNotFound
		}
		if _, ok := err.(domain.ErrUnknownCommand); ok {
			code = http.StatusBadRequest
		}
		c.JSON(code, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) DeleteInstance(c *gin.Context) {
	ctx := c.Request.Context()

	if err := h.vm.Stop(ctx); err != nil {
		h.logger.Error("failed to stop instance", "err", err)
	}

	if err := h.frpc.ClearInstanceProxies(); err != nil {
		h.logger.Error("failed to clear frpc proxies", "err", err)
	}

	if err := h.store.ClearInstanceState(); err != nil {
		h.logger.Error("failed to clear instance state", "err", err)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type sshRequest struct {
	SSHPubkey string `json:"ssh_pubkey" binding:"required"`
}

func (h *Handler) AddSSH(c *gin.Context) {
	var req sshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("add ssh key: bad request", "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	h.logger.Info("adding SSH key", "pubkey_prefix", truncate(req.SSHPubkey, 40))
	if err := h.vm.AddSSHKey(c.Request.Context(), req.SSHPubkey); err != nil {
		h.logger.Error("add ssh key failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	h.logger.Info("SSH key added successfully")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) RemoveSSH(c *gin.Context) {
	var req sshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := h.vm.RemoveSSHKey(c.Request.Context(), req.SSHPubkey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
