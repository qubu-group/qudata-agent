package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/frpc"
	"github.com/qudata/agent/internal/network"
	"github.com/qudata/agent/internal/storage"
)

// Handler implements all HTTP endpoint handlers.
type Handler struct {
	vm        domain.VMManager
	frpc      *frpc.Process
	ports     *network.PortAllocator
	store     *storage.Store
	logger    *slog.Logger
	subdomain string
}

// NewHandler creates a new request handler.
func NewHandler(
	vm domain.VMManager,
	frpc *frpc.Process,
	ports *network.PortAllocator,
	store *storage.Store,
	logger *slog.Logger,
	subdomain string,
) *Handler {
	return &Handler{
		vm:        vm,
		frpc:      frpc,
		ports:     ports,
		store:     store,
		logger:    logger,
		subdomain: subdomain,
	}
}

// Ping is a health-check endpoint.
func (h *Handler) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// createInstanceRequest is the JSON body for POST /instances.
type createInstanceRequest struct {
	Image      string            `json:"image" binding:"required"`
	ImageTag   string            `json:"image_tag"`
	Registry   string            `json:"registry"`
	Login      string            `json:"login"`
	Password   string            `json:"password"`
	EnvVars    map[string]string `json:"env_variables"`
	Ports      []portRequest     `json:"ports" binding:"required"`
	Command    string            `json:"command"`
	SSHEnabled bool              `json:"ssh_enabled"`
	StorageGB  int               `json:"storage_gb"`
	GPUAddr    string            `json:"gpu_addr"`
	DiskSizeGB int               `json:"disk_size_gb"`
}

type portRequest struct {
	ContainerPort int    `json:"container_port" binding:"required"`
	RemotePort    int    `json:"remote_port" binding:"required"`
	Proto         string `json:"proto" binding:"required"`
}

// CreateInstance provisions a new VM instance asynchronously.
func (h *Handler) CreateInstance(c *gin.Context) {
	var req createInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	spec := domain.InstanceSpec{
		Image:      req.Image,
		ImageTag:   req.ImageTag,
		Registry:   req.Registry,
		Login:      req.Login,
		Password:   req.Password,
		EnvVars:    req.EnvVars,
		Command:    req.Command,
		SSHEnabled: req.SSHEnabled,
		StorageGB:  req.StorageGB,
		GPUAddr:    req.GPUAddr,
		DiskSizeGB: req.DiskSizeGB,
	}
	for _, p := range req.Ports {
		spec.Ports = append(spec.Ports, domain.PortMapping{
			ContainerPort: p.ContainerPort,
			RemotePort:    p.RemotePort,
			Proto:         p.Proto,
		})
	}

	hostPorts, err := h.ports.Allocate(len(spec.Ports))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "port allocation failed: " + err.Error()})
		return
	}

	go h.startInstance(context.Background(), spec, hostPorts)

	portResult := make(map[int]int)
	for _, pm := range spec.Ports {
		portResult[pm.ContainerPort] = pm.RemotePort
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"data": gin.H{"ports": portResult},
	})
}

func (h *Handler) startInstance(ctx context.Context, spec domain.InstanceSpec, hostPorts []int) {
	portMap, err := h.vm.Create(ctx, spec, hostPorts)
	if err != nil {
		h.logger.Error("failed to create instance", "err", err)
		return
	}

	frpProxies := frpc.BuildInstanceProxies(spec, hostPorts, h.subdomain)
	if err := h.frpc.UpdateInstanceProxies(frpProxies); err != nil {
		h.logger.Error("failed to update frpc proxies", "err", err)
	}

	state := &domain.InstanceState{
		ContainerID: h.vm.VMID(),
		Image:       spec.Image,
		Ports:       portMap,
		FRPProxies:  frpProxies,
		SSHEnabled:  spec.SSHEnabled,
		GPUAddr:     spec.GPUAddr,
	}
	if err := h.store.SaveInstanceState(state); err != nil {
		h.logger.Error("failed to save instance state", "err", err)
	}

	h.logger.Info("instance created successfully", "ports", portMap)
}

// GetInstance returns the current instance status.
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

// ManageInstance executes a lifecycle command on the running instance.
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

// DeleteInstance stops and removes the running instance.
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

// AddSSH installs an SSH public key in the running instance.
func (h *Handler) AddSSH(c *gin.Context) {
	var req sshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if err := h.vm.AddSSHKey(c.Request.Context(), req.SSHPubkey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RemoveSSH removes an SSH public key from the running instance.
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
