//go:build linux && cgo

package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

/*
#cgo LDFLAGS: -lnvidia-ml
const char* getGpuName();
*/
import "C"

// GetFingerprint returns unique fingerprint
func GetFingerprint() string {
	var parts []string

	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		parts = append(parts, strings.TrimSpace(string(b)))
	}

	if name := C.getGpuName(); name != nil {
		parts = append(parts, C.GoString(name))
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
