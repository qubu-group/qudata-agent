package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/magicaleks/qudata-agent-alpha/pkg/containers"
	"github.com/magicaleks/qudata-agent-alpha/pkg/utils"
)

type response struct {
	Ok   bool `json:"ok"`
	Data any  `json:"data"`
}

type sshKeyRequest struct {
	SSHPubkey string `json:"ssh_pubkey"`
}

type createInstanceRequest struct {
	Image      string            `json:"image"`
	ImageTag   string            `json:"image_tag"`
	StorageGB  int               `json:"storage_gb"`
	Registry   string            `json:"registry"`
	Login      string            `json:"login"`
	Password   string            `json:"password"`
	EnvVars    map[string]string `json:"env_variables"`
	Ports      map[string]string `json:"ports"`
	Command    string            `json:"command"`
	SSHEnabled bool              `json:"ssh_enabled"`
}

type instanceCreatedResponse struct {
	Ports map[string]string `json:"ports"`
}

type manageInstanceRequest struct {
	Command string `json:"command"`
}

type instanceStatusResponse struct {
	Status containers.InstanceStatus `json:"status"`
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		_, _ = w.Write(resp)
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)

}

func sshHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req sshKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils.LogWarn("ssh add: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := containers.AddSSH(req.SSHPubkey); err != nil {
			utils.LogWarn("ssh add failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils.LogInfo("ssh key added")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	if r.Method == "DELETE" {
		var req sshKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils.LogWarn("ssh remove: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := containers.RemoveSSH(req.SSHPubkey); err != nil {
			utils.LogWarn("ssh remove failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils.LogInfo("ssh key removed")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func instancesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		status := containers.GetInstanceStatus()
		resp, _ := json.Marshal(response{Ok: true, Data: instanceStatusResponse{Status: status}})
		w.Write(resp)
		return
	}
	if r.Method == "POST" {
		var req createInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils.LogWarn("create instance: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		image := req.Image
		if req.ImageTag != "" {
			image += ":" + req.ImageTag
		}

		allocatedPorts := make(map[string]string)
		if len(req.Ports) > 0 {
			firstPort, _ := utils.GetPortsRange(len(req.Ports))
			if firstPort == 0 {
				utils.LogError("failed to allocate ports")
				http.Error(w, "failed to allocate ports", http.StatusInternalServerError)
				return
			}

			portIdx := 0
			for containerPort := range req.Ports {
				hostPort := strconv.Itoa(firstPort + portIdx)
				allocatedPorts[containerPort] = hostPort
				portIdx++
			}
		}

		createData := containers.CreateInstance{
			Image:      image,
			VolumeSize: strconv.Itoa(req.StorageGB * 1024),
			Registry:   req.Registry,
			Login:      req.Login,
			Password:   req.Password,
			EnvVars:    req.EnvVars,
			Ports:      allocatedPorts,
			Command:    req.Command,
			SSHEnabled: req.SSHEnabled,
		}

		go func() {
			if err := containers.StartInstance(createData); err != nil {
				utils.LogError("failed to start instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}()

		utils.LogInfo("instance created: %s", image)
		resp, _ := json.Marshal(response{Ok: true, Data: instanceCreatedResponse{Ports: allocatedPorts}})
		w.Write(resp)
		return
	}
	if r.Method == "PUT" {
		var req manageInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils.LogWarn("manage instance: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Command {
		case "delete":
			if err := containers.StopInstance(); err != nil {
				utils.LogError("failed to delete instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils.LogInfo("instance deleted")
		case "start":
			if err := containers.ManageInstance(containers.StartCommand); err != nil {
				utils.LogError("failed to start instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils.LogInfo("instance started")
		case "stop":
			if err := containers.ManageInstance(containers.StopCommand); err != nil {
				utils.LogError("failed to stop instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils.LogInfo("instance stopped")
		case "restart":
			if err := containers.ManageInstance(containers.RebootCommand); err != nil {
				utils.LogError("failed to restart instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils.LogInfo("instance restarted")
		default:
			utils.LogWarn("unknown command: %s", req.Command)
			http.Error(w, "unknown command", http.StatusBadRequest)
			return
		}

		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	if r.Method == "DELETE" {
		if err := containers.StopInstance(); err != nil {
			utils.LogError("failed to delete instance: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils.LogInfo("instance deleted")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}
