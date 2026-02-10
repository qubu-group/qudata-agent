package qemu

import (
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
