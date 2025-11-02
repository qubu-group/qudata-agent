package server

import (
	"encoding/json"
	containers2 "github.com/magicaleks/qudata-agent-alpha/internal/containers"
	utils2 "github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"math"
	"net/http"
	"strconv"
	"strings"
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
	Ports      []string          `json:"ports"`
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
	Status containers2.InstanceStatus `json:"status"`
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
			utils2.LogWarn("ssh add: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := containers2.AddSSH(req.SSHPubkey); err != nil {
			utils2.LogWarn("ssh add failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils2.LogInfo("ssh key added")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	if r.Method == "DELETE" {
		var req sshKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils2.LogWarn("ssh remove: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := containers2.RemoveSSH(req.SSHPubkey); err != nil {
			utils2.LogWarn("ssh remove failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils2.LogInfo("ssh key removed")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func instancesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		status := containers2.GetInstanceStatus()
		resp, _ := json.Marshal(response{Ok: true, Data: instanceStatusResponse{Status: status}})
		w.Write(resp)
		return
	}
	if r.Method == "POST" {
		var req createInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils2.LogWarn("create instance: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		image := req.Image
		if image == "" {
			image = "ubuntu:22.04"
		} else if req.ImageTag != "" && !strings.Contains(image, ":") {
			image += ":" + req.ImageTag
		}

		allocatedPorts := make(map[string]string)
		if len(req.Ports) > 0 {
			firstPort, _ := utils2.GetPortsRange(len(req.Ports))
			if firstPort == 0 {
				utils2.LogError("failed to allocate ports")
				http.Error(w, "failed to allocate ports", http.StatusInternalServerError)
				return
			}

			portIdx := 0
			for _, containerPort := range req.Ports {
				hostPort := strconv.Itoa(firstPort + portIdx)
				allocatedPorts[containerPort] = hostPort
				portIdx++
			}
		}

		conf := utils2.GetConfiguration()

		createData := containers2.CreateInstance{
			Image:      image,
			VolumeSize: int64(math.Min(float64(req.StorageGB*1024), conf.Disk.Amount*1024)),
			Registry:   req.Registry,
			Login:      req.Login,
			Password:   req.Password,
			EnvVars:    req.EnvVars,
			Ports:      allocatedPorts,
			Command:    req.Command,
			SSHEnabled: req.SSHEnabled,
		}

		go func() {
			if err := containers2.StartInstance(createData); err != nil {
				utils2.LogError("failed to start instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}()

		utils2.LogInfo("instance created: %s", image)
		resp, _ := json.Marshal(response{Ok: true, Data: instanceCreatedResponse{Ports: allocatedPorts}})
		w.Write(resp)
		return
	}
	if r.Method == "PUT" {
		var req manageInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils2.LogWarn("manage instance: invalid request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch req.Command {
		case "delete":
			if err := containers2.StopInstance(); err != nil {
				utils2.LogError("failed to delete instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils2.LogInfo("instance deleted")
		case "start":
			if err := containers2.ManageInstance(containers2.StartCommand); err != nil {
				utils2.LogError("failed to start instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils2.LogInfo("instance started")
		case "stop":
			if err := containers2.ManageInstance(containers2.StopCommand); err != nil {
				utils2.LogError("failed to stop instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils2.LogInfo("instance stopped")
		case "restart":
			if err := containers2.ManageInstance(containers2.RebootCommand); err != nil {
				utils2.LogError("failed to restart instance: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			utils2.LogInfo("instance restarted")
		default:
			utils2.LogWarn("unknown command: %s", req.Command)
			http.Error(w, "unknown command", http.StatusBadRequest)
			return
		}

		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	if r.Method == "DELETE" {
		if err := containers2.StopInstance(); err != nil {
			utils2.LogError("failed to delete instance: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		utils2.LogInfo("instance deleted")
		resp, _ := json.Marshal(response{Ok: true, Data: nil})
		w.Write(resp)
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}
