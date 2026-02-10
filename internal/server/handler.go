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
	Ports        []portRequest `json:"ports" binding:"required"`
	SSHEnabled   bool          `json:"ssh_enabled"`
	SecretDomain string        `json:"secret_domain" binding:"required"`
	DiskSizeGB   int           `json:"disk_size_gb"`
	CPUs         string        `json:"cpus"`
	Memory       string        `json:"memory"`
}

type portRequest struct {
	GuestPort  int    `json:"guest_port" binding:"required"`
	RemotePort int    `json:"remote_port" binding:"required"`
	Proto      string `json:"proto" binding:"required"`
}

func (h *Handler) CreateInstance(c *gin.Context) {
	var req createInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	spec := domain.InstanceSpec{
		SSHEnabled:   req.SSHEnabled,
		SecretDomain: req.SecretDomain,
		DiskSizeGB:   req.DiskSizeGB,
		CPUs:         req.CPUs,
		Memory:       req.Memory,
	}
	for _, p := range req.Ports {
		spec.Ports = append(spec.Ports, domain.PortMapping{
			GuestPort:  p.GuestPort,
			RemotePort: p.RemotePort,
			Proto:      p.Proto,
		})
	}

	portsNeeded := len(spec.Ports)
	if spec.SSHEnabled {
		portsNeeded++
	}

	var hostPorts []int
	var sshRemotePort int

	if spec.SSHEnabled {
		sshPort, err := h.ports.AllocateSSHPort()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "ssh port allocation: " + err.Error()})
			return
		}
		sshRemotePort = sshPort

		localSSHPort, err := h.ports.AllocateAppPorts(1)
		if err != nil {
			h.ports.Release(sshPort)
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "local ssh port allocation: " + err.Error()})
			return
		}
		hostPorts = append(hostPorts, localSSHPort[0])
	}

	if len(spec.Ports) > 0 {
		appPorts, err := h.ports.AllocateAppPorts(len(spec.Ports))
		if err != nil {
			h.ports.Release(hostPorts...)
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "app port allocation: " + err.Error()})
			return
		}
		hostPorts = append(hostPorts, appPorts...)
	}

	go h.startInstance(context.Background(), spec, hostPorts, sshRemotePort)

	portResult := make(map[string]int)
	if spec.SSHEnabled {
		portResult["ssh"] = sshRemotePort
	}
	for _, pm := range spec.Ports {
		portResult[strconv.Itoa(pm.GuestPort)] = pm.RemotePort
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"data": gin.H{"ports": portResult},
	})
}

func (h *Handler) startInstance(ctx context.Context, spec domain.InstanceSpec, hostPorts []int, sshRemotePort int) {
	portMap, err := h.vm.Create(ctx, spec, hostPorts)
	if err != nil {
		h.logger.Error("failed to create instance", "err", err)
		return
	}

	frpProxies := frpc.BuildInstanceProxies(spec, hostPorts, sshRemotePort)
	if err := h.frpc.UpdateInstanceProxies(frpProxies); err != nil {
		h.logger.Error("failed to update frpc proxies", "err", err)
	}

	state := &domain.InstanceState{
		VMID:         h.vm.VMID(),
		Ports:        portMap,
		FRPProxies:   frpProxies,
		SSHEnabled:   spec.SSHEnabled,
		GPUAddr:      spec.GPUAddr,
		SecretDomain: spec.SecretDomain,
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
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := h.vm.AddSSHKey(c.Request.Context(), req.SSHPubkey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
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
