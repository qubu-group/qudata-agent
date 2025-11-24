//go:build linux && cgo

package system

/*
#cgo LDFLAGS: -lnvidia-ml

const char* getGpuSerial();
*/
import "C"

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

func machineFingerprint() string {
	var parts []string

	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		parts = append(parts, strings.TrimSpace(string(b)))
	}

	if serial := C.getGpuSerial(); serial != nil {
		parts = append(parts, C.GoString(serial))
	}

	logger.LogInfo("GPU fingerprint: %s", strings.Join(parts, ", "))

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
