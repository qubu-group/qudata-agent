//go:build linux && cgo

package utils

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
)

// GetFingerprint returns unique fingerprint
func GetFingerprint() string {
	var parts []string

	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		parts = append(parts, strings.TrimSpace(string(b)))
	}

	if serial := C.getGpuSerial(); name != nil {
		parts = append(parts, C.GoString(serial))
	}

	LogInfo("GPU fingerprint:", strings.Join(parts, ", "))

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
