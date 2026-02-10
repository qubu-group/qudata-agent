// Package ssh provides SSH key generation and management utilities
// for the Qudata agent's management access to QEMU VMs.
package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	// DefaultKeyDir is the default directory for management keys.
	DefaultKeyDir = "/var/lib/qudata/.ssh"

	// PrivateKeyFile is the filename for the private key.
	PrivateKeyFile = "management_key"

	// PublicKeyFile is the filename for the public key.
	PublicKeyFile = "management_key.pub"

	// KeyComment is the comment added to the public key.
	KeyComment = "qudata-management"
)

// KeyPair holds paths to an SSH key pair.
type KeyPair struct {
	PrivateKeyPath string
	PublicKeyPath  string
}

// EnsureManagementKey checks for existing management keys and generates them if missing.
// Returns the paths to the private and public keys.
func EnsureManagementKey(keyDir string) (*KeyPair, error) {
	if keyDir == "" {
		keyDir = DefaultKeyDir
	}

	privateKeyPath := filepath.Join(keyDir, PrivateKeyFile)
	publicKeyPath := filepath.Join(keyDir, PublicKeyFile)

	// Check if keys already exist
	if fileExists(privateKeyPath) && fileExists(publicKeyPath) {
		// Validate the existing keys
		if err := validateKeyPair(privateKeyPath, publicKeyPath); err != nil {
			return nil, fmt.Errorf("existing keys are invalid: %w (remove them to regenerate)", err)
		}
		return &KeyPair{
			PrivateKeyPath: privateKeyPath,
			PublicKeyPath:  publicKeyPath,
		}, nil
	}

	// Create key directory with restricted permissions
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}

	// Generate new key pair
	if err := generateED25519KeyPair(privateKeyPath, publicKeyPath); err != nil {
		return nil, fmt.Errorf("generate key pair: %w", err)
	}

	return &KeyPair{
		PrivateKeyPath: privateKeyPath,
		PublicKeyPath:  publicKeyPath,
	}, nil
}

// ReadPublicKey reads the public key from a file and returns it as a string.
func ReadPublicKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// generateED25519KeyPair creates a new Ed25519 key pair and writes it to files.
func generateED25519KeyPair(privateKeyPath, publicKeyPath string) error {
	// Generate Ed25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Convert to SSH format
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("convert public key to ssh format: %w", err)
	}

	// Marshal public key in authorized_keys format
	pubKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)
	// Add comment
	pubKeyStr := strings.TrimSpace(string(pubKeyBytes)) + " " + KeyComment + "\n"

	// Marshal private key in OpenSSH format
	privKeyPEM, err := ssh.MarshalPrivateKey(privKey, KeyComment)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}

	// Write private key with restricted permissions
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(privKeyPEM), 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	// Write public key
	if err := os.WriteFile(publicKeyPath, []byte(pubKeyStr), 0o644); err != nil {
		// Clean up private key on failure
		_ = os.Remove(privateKeyPath)
		return fmt.Errorf("write public key: %w", err)
	}

	return nil
}

// validateKeyPair checks that the key files are readable and valid.
func validateKeyPair(privateKeyPath, publicKeyPath string) error {
	// Read and parse private key
	privData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return fmt.Errorf("read private key: %w", err)
	}

	_, err = ssh.ParsePrivateKey(privData)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	// Read and parse public key
	pubData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return fmt.Errorf("read public key: %w", err)
	}

	_, _, _, _, err = ssh.ParseAuthorizedKey(pubData)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}

	return nil
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
