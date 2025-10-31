//go:build !linux || !cgo

package utils

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

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
