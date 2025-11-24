package httpserver

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
	instanceuc "github.com/magicaleks/qudata-agent-alpha/internal/usecase/instance"
	"github.com/magicaleks/qudata-agent-alpha/internal/usecase/maintenance"
)

type response struct {
	Ok    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type sshKeyRequest struct {
	SSHPubKey string `json:"ssh_pubkey"`
}

type createInstanceRequest struct {
	Image       string            `json:"image"`
	ImageTag    string            `json:"image_tag"`
	StorageGB   int               `json:"storage_gb"`
	Registry    string            `json:"registry"`
	Login       string            `json:"login"`
	Password    string            `json:"password"`
	EnvVars     map[string]string `json:"env_variables"`
	Ports       []string          `json:"ports"`
	Command     string            `json:"command"`
	SSHEnabled  bool              `json:"ssh_enabled"`
	TunnelToken string            `json:"tunnel_token"`
}

type instanceCreatedResponse struct {
	Ports map[string]string `json:"ports"`
}

type manageInstanceRequest struct {
	Command string `json:"command"`
}

type instanceStatusResponse struct {
	Status domain.InstanceStatus `json:"status"`
}

type API struct {
	instances *instanceuc.Service
	updater   *maintenance.Updater
	logger    impls.Logger
}

func NewAPI(instances *instanceuc.Service, updater *maintenance.Updater, logger impls.Logger) *API {
	return &API{instances: instances, updater: updater, logger: logger}
}

func (a *API) RegisterRoutes(router *gin.Engine) {
	router.GET("/ping", a.ping)
	router.GET("/instances", a.instanceStatus)
	router.POST("/instances", a.createInstance)
	router.PUT("/instances", a.manageInstance)
	router.DELETE("/instances", a.deleteInstance)
	router.POST("/ssh", a.addSSH)
	router.DELETE("/ssh", a.removeSSH)
	router.POST("/self-update", a.selfUpdate)
}

func (a *API) ping(c *gin.Context) {
	c.JSON(http.StatusOK, response{Ok: true})
}

func (a *API) instanceStatus(c *gin.Context) {
	status := a.instances.Status(c.Request.Context())
	c.JSON(http.StatusOK, response{Ok: true, Data: instanceStatusResponse{Status: status}})
}

func (a *API) createInstance(c *gin.Context) {
	var req createInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.logger.Warn("create instance: invalid payload: %v", err)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: err.Error()})
		return
	}

	if strings.TrimSpace(req.TunnelToken) == "" {
		msg := "tunnel_token is required"
		a.logger.Warn("create instance: %s", msg)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: msg})
		return
	}

	input := instanceuc.CreateInput{
		Image:       req.Image,
		ImageTag:    req.ImageTag,
		StorageGB:   req.StorageGB,
		Registry:    req.Registry,
		Login:       req.Login,
		Password:    req.Password,
		EnvVars:     req.EnvVars,
		Ports:       req.Ports,
		Command:     req.Command,
		SSHEnabled:  req.SSHEnabled,
		TunnelToken: req.TunnelToken,
	}

	ports, err := a.instances.Create(c.Request.Context(), input)
	if err != nil {
		a.logger.Error("create instance failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, response{Ok: true, Data: instanceCreatedResponse{Ports: ports}})
}

func (a *API) manageInstance(c *gin.Context) {
	var req manageInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.logger.Warn("manage instance: invalid payload: %v", err)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: err.Error()})
		return
	}

	cmd := parseCommand(req.Command)
	if cmd == domain.CommandUnknown {
		a.logger.Warn("manage instance: unknown command %s", req.Command)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: "unknown command"})
		return
	}

	var err error
	if cmd == domain.CommandDelete {
		err = a.instances.Delete(c.Request.Context())
	} else {
		err = a.instances.Manage(c.Request.Context(), cmd)
	}
	if err != nil {
		a.logger.Error("manage instance failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, response{Ok: true})
}

func (a *API) deleteInstance(c *gin.Context) {
	if err := a.instances.Delete(c.Request.Context()); err != nil {
		a.logger.Error("delete instance failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, response{Ok: true})
}

func (a *API) addSSH(c *gin.Context) {
	var req sshKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.logger.Warn("add ssh: invalid payload: %v", err)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: err.Error()})
		return
	}
	if err := a.instances.AddSSH(c.Request.Context(), req.SSHPubKey); err != nil {
		a.logger.Error("add ssh failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, response{Ok: true})
}

func (a *API) removeSSH(c *gin.Context) {
	var req sshKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.logger.Warn("remove ssh: invalid payload: %v", err)
		c.JSON(http.StatusBadRequest, response{Ok: false, Error: err.Error()})
		return
	}
	if err := a.instances.RemoveSSH(c.Request.Context(), req.SSHPubKey); err != nil {
		a.logger.Error("remove ssh failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, response{Ok: true})
}

func (a *API) selfUpdate(c *gin.Context) {
	if err := a.updater.Run(c.Request.Context()); err != nil {
		a.logger.Error("self-update failed: %v", err)
		c.JSON(http.StatusInternalServerError, response{Ok: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, response{Ok: true})
}

func parseCommand(cmd string) domain.InstanceCommand {
	switch cmd {
	case "start":
		return domain.CommandStart
	case "stop":
		return domain.CommandStop
	case "restart":
		return domain.CommandReboot
	case "delete":
		return domain.CommandDelete
	default:
		return domain.CommandUnknown
	}
}
