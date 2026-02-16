package qemu

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ImageManager struct {
	imageDir string
}

func NewImageManager(imageDir string) *ImageManager {
	return &ImageManager{imageDir: imageDir}
}

func (m *ImageManager) CreateDisk(name string, sizeGB int) (string, error) {
	if err := os.MkdirAll(m.imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}
	path := filepath.Join(m.imageDir, name+".qcow2")
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qemu-img create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

func (m *ImageManager) CreateOverlay(name, basePath string) (string, error) {
	if err := os.MkdirAll(m.imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}
	if _, err := os.Stat(basePath); err != nil {
		return "", fmt.Errorf("base image not found: %w", err)
	}
	path := filepath.Join(m.imageDir, name+".qcow2")
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", basePath, "-F", "qcow2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qemu-img create overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// ResizeDisk grows the disk to the requested size. If the disk is already
// equal or larger, the call is a no-op (we never shrink).
func (m *ImageManager) ResizeDisk(path string, sizeGB int) error {
	currentBytes, err := m.virtualSize(path)
	if err != nil {
		return fmt.Errorf("qemu-img resize: query size: %w", err)
	}

	targetBytes := int64(sizeGB) * 1024 * 1024 * 1024
	if targetBytes <= currentBytes {
		return nil
	}

	cmd := exec.Command("qemu-img", "resize", path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img resize: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// virtualSize returns the virtual disk size in bytes via qemu-img info.
func (m *ImageManager) virtualSize(path string) (int64, error) {
	cmd := exec.Command("qemu-img", "info", "--output=json", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("qemu-img info: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var info struct {
		VirtualSize int64 `json:"virtual-size"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return 0, fmt.Errorf("parse qemu-img info: %w", err)
	}
	return info.VirtualSize, nil
}

func (m *ImageManager) RemoveDisk(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove disk %s: %w", path, err)
	}
	return nil
}

func (m *ImageManager) DiskPath(name string) string {
	return filepath.Join(m.imageDir, name+".qcow2")
}

func (m *ImageManager) DiskExists(name string) bool {
	_, err := os.Stat(m.DiskPath(name))
	return err == nil
}
