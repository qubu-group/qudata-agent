package internal

import (
	"bufio"
	"github.com/google/uuid"
	"github.com/magicaleks/qudata-agent-alpha/pkg/utils"
	"os"
	"strings"
)

// GetAgentId create or restore agent id
func GetAgentId() string {
	var agentId string
	file, err := os.OpenFile(AgentIdPATH, os.O_RDONLY, 0666)
	if err == nil {
		buf := bufio.NewReader(file)
		stored, _ := buf.ReadBytes('\n')
		storedId, err := uuid.FromBytes(stored)
		if err == nil {
			agentId = storedId.String()
		}
	}
	if agentId == "" {
		agentIdUUID := uuid.New()
		agentId = agentIdUUID.String()
		store, _ := agentIdUUID.MarshalBinary()
		err = os.WriteFile(AgentIdPATH, store, 0666)
		if err != nil {
			utils.LogError(err.Error())
		}
	}
	return agentId
}

// GetSecretKey returns agent secret key
func GetSecretKey() string {
	var secret string
	file, err := os.OpenFile(AgentSecretPATH, os.O_RDONLY, 0666)
	if err == nil {
		buf := bufio.NewReader(file)
		secret, _ = buf.ReadString('\n')
	}
	if !strings.HasPrefix(secret, "sk-") {
		secret = ""
		_ = os.Remove(AgentSecretPATH)
	}
	return secret
}

// SetSecretKey sets agent secret key
func SetSecretKey(secret string) {
	err := os.WriteFile(AgentSecretPATH, []byte(secret), 0666)
	if err != nil {
		utils.LogError(err.Error())
	}
}
