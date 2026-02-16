package server

import (
	"bytes"
	"context"
	"io"
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
	vm       domain.VMManager
	frpc     *frpc.Process
	ports    *network.PortAllocator
	store    *storage.Store
	logger   *slog.Logger
	testMode bool
}

func NewHandler(
	vm domain.VMManager,
	frpc *frpc.Process,
	ports *network.PortAllocator,
	store *storage.Store,
	logger *slog.Logger,
	testMode bool,
) *Handler {
	return &Handler{
		vm:       vm,
		frpc:     frpc,
		ports:    ports,
		store:    store,
		logger:   logger,
		testMode: testMode,
	}
}

func (h *Handler) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type createInstanceRequest struct {
	TunnelToken  string            `json:"tunnel_token"` // required only in non-test mode
	SSHEnabled   bool              `json:"ssh_enabled"`
	Ports        []string          `json:"ports"` // e.g. ["22", "8080"]
	StorageGB    int               `json:"storage_gb"`
	Image        string            `json:"image"`
	ImageTag     string            `json:"image_tag"`
	Registry     *string           `json:"registry"`
	Login        *string           `json:"login"`
	Password     *string           `json:"password"`
	EnvVariables map[string]string `json:"env_variables"`
	Command      *string           `json:"command"`
	CPUs         string            `json:"cpus"`
	Memory       string            `json:"memory"`
}

func (h *Handler) CreateInstance(c *gin.Context) {
	// Read raw body for logging
	bodyBytes, _ := io.ReadAll(c.Request.Body)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	h.logger.Info("CreateInstance request",
		"body", string(bodyBytes),
		"content_type", c.ContentType(),
	)

	var req createInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("CreateInstance bind error",
			"error", err.Error(),
			"body", string(bodyBytes),
		)
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	h.logger.Info("CreateInstance parsed",
		"tunnel_token", req.TunnelToken,
		"ssh_enabled", req.SSHEnabled,
		"ports", len(req.Ports),
		"test_mode", h.testMode,
	)

	if h.testMode {
		h.createTestInstance(c, req)
	} else {
		h.createFRPCInstance(c, req)
	}
}

// createTestInstance — hardcoded SSH + Ollama, ports on 0.0.0.0, no FRPC.
func (h *Handler) createTestInstance(c *gin.Context, req createInstanceRequest) {
	sshPort, err := h.ports.AllocateSSHPort()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	ollamaPort, err := h.ports.AllocateOne()
	if err != nil {
		h.ports.Release(sshPort)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	spec := domain.InstanceSpec{
		SSHEnabled:  true,
		TunnelToken: req.TunnelToken,
		DiskSizeGB:  req.StorageGB,
		CPUs:        req.CPUs,
		Memory:      req.Memory,
		Ports: []domain.PortMapping{
			{Name: "ollama", GuestPort: 11434, Proto: "http"},
		},
	}
	hostPorts := []int{sshPort, ollamaPort}

	allocated := []int{sshPort, ollamaPort}
	go h.startVM(context.Background(), spec, hostPorts, allocated)

	h.logger.Info("instance creating (test)", "ssh", sshPort, "ollama", ollamaPort)
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"data": gin.H{
			"ports": gin.H{
				"22":    strconv.Itoa(sshPort),
				"11434": strconv.Itoa(ollamaPort),
			},
		},
	})
}

// createFRPCInstance — dynamic ports from request, tunneled via FRPC.
func (h *Handler) createFRPCInstance(c *gin.Context, req createInstanceRequest) {
	if req.TunnelToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "tunnel_token is required"})
		return
	}

	var (
		hostPorts    []int
		allocated    []int
		portMappings []domain.PortMapping
		sshRemote    int
	)

	rollback := func() { h.ports.Release(allocated...) }

	if req.SSHEnabled {
		remote, err := h.ports.AllocateSSHPort()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		allocated = append(allocated, remote)

		local, err := h.ports.AllocateOne()
		if err != nil {
			rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		allocated = append(allocated, local)

		sshRemote = remote
		hostPorts = append(hostPorts, local)
	}

	for _, portStr := range req.Ports {
		if portStr == "22" && req.SSHEnabled {
			continue
		}

		guestPort, err := strconv.Atoi(portStr)
		if err != nil {
			rollback()
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid port: " + portStr})
			return
		}

		local, err := h.ports.AllocateOne()
		if err != nil {
			rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		allocated = append(allocated, local)

		// Auto-detect: port 22 = TCP (unique remote port), everything else = HTTP (vhost routing).
		proto := "http"
		if guestPort == 22 {
			proto = "tcp"
		}

		var remote int
		if proto == "tcp" {
			remote, err = h.ports.AllocateSSHPort()
		} else {
			remote, err = h.ports.AllocateOne()
		}
		if err != nil {
			rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		allocated = append(allocated, remote)

		hostPorts = append(hostPorts, local)
		portMappings = append(portMappings, domain.PortMapping{
			Name:       "port-" + portStr,
			GuestPort:  guestPort,
			RemotePort: remote,
			Proto:      proto,
		})
	}

	spec := domain.InstanceSpec{
		SSHEnabled:  req.SSHEnabled,
		TunnelToken: req.TunnelToken,
		DiskSizeGB:  req.StorageGB,
		CPUs:        req.CPUs,
		Memory:      req.Memory,
		Ports:       portMappings,
	}

	go h.startVMWithFRPC(context.Background(), spec, hostPorts, sshRemote, allocated)

	// Build response: guest_port → remote_port (what clients connect to via FRPC).
	ports := make(gin.H, len(portMappings)+1)
	if req.SSHEnabled {
		ports["22"] = strconv.Itoa(sshRemote)
	}
	for _, pm := range portMappings {
		ports[strconv.Itoa(pm.GuestPort)] = strconv.Itoa(pm.RemotePort)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "data": gin.H{"ports": ports}})
}

// ---------------------------------------------------------------------------
// VM lifecycle (background)
// ---------------------------------------------------------------------------

func (h *Handler) startVM(ctx context.Context, spec domain.InstanceSpec, hostPorts, allocated []int) {
	portMap, err := h.vm.Create(ctx, spec, hostPorts)
	if err != nil {
		h.logger.Error("instance creation failed", "err", err)
		return
	}

	h.saveState(spec, portMap, allocated)
	h.logger.Info("instance running", "vm_id", h.vm.VMID(), "ports", portMap)
}

func (h *Handler) startVMWithFRPC(ctx context.Context, spec domain.InstanceSpec, hostPorts []int, sshRemote int, allocated []int) {
	portMap, err := h.vm.Create(ctx, spec, hostPorts)
	if err != nil {
		h.logger.Error("instance creation failed", "err", err)
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

	proxies := frpc.BuildInstanceProxies(spec.TunnelToken, hostPorts, sshRemote, spec.SSHEnabled, portSpecs)
	if err := h.frpc.UpdateInstanceProxies(proxies); err != nil {
		h.logger.Error("frpc proxy update failed", "err", err)
	}

	h.saveState(spec, portMap, allocated)
	h.logger.Info("instance running", "vm_id", h.vm.VMID(), "ports", portMap)
}

func (h *Handler) saveState(spec domain.InstanceSpec, portMap domain.InstancePorts, allocated []int) {
	state := &domain.InstanceState{
		VMID:           h.vm.VMID(),
		Ports:          portMap,
		SSHEnabled:     spec.SSHEnabled,
		GPUAddr:        spec.GPUAddr,
		TunnelToken:    spec.TunnelToken,
		AllocatedPorts: allocated,
	}
	if err := h.store.SaveInstanceState(state); err != nil {
		h.logger.Error("failed to save instance state", "err", err)
	}
}

// ---------------------------------------------------------------------------
// Instance management
// ---------------------------------------------------------------------------

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
	state, _ := h.store.LoadInstanceState()

	if err := h.vm.Stop(c.Request.Context()); err != nil {
		h.logger.Error("failed to stop instance", "err", err)
	}

	if err := h.frpc.ClearInstanceProxies(); err != nil {
		h.logger.Error("failed to clear frpc proxies", "err", err)
	}

	if state != nil && len(state.AllocatedPorts) > 0 {
		h.ports.Release(state.AllocatedPorts...)
	}

	if err := h.store.ClearInstanceState(); err != nil {
		h.logger.Error("failed to clear instance state", "err", err)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// SSH key management
// ---------------------------------------------------------------------------

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
		h.logger.Error("add ssh key failed", "err", err)
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
