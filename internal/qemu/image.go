package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ImageManager handles qcow2 disk image creation, overlay management,
// and conversion from Docker images.
type ImageManager struct {
	imageDir string
}

// NewImageManager creates an image manager that stores disks under imageDir.
func NewImageManager(imageDir string) *ImageManager {
	return &ImageManager{imageDir: imageDir}
}

// CreateDisk creates a new empty qcow2 disk image with the specified size.
func (m *ImageManager) CreateDisk(name string, sizeGB int) (string, error) {
	if err := os.MkdirAll(m.imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	path := filepath.Join(m.imageDir, name+".qcow2")
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qemu-img create: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return path, nil
}

// CreateOverlay creates a copy-on-write qcow2 image backed by basePath.
// Writes go to the overlay while the base image remains read-only.
func (m *ImageManager) CreateOverlay(name, basePath string) (string, error) {
	if err := os.MkdirAll(m.imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	if _, err := os.Stat(basePath); err != nil {
		return "", fmt.Errorf("base image not found: %w", err)
	}

	path := filepath.Join(m.imageDir, name+".qcow2")
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2",
		"-b", basePath,
		"-F", "qcow2",
		path)

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qemu-img create overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return path, nil
}

// RemoveDisk deletes a disk image file.
func (m *ImageManager) RemoveDisk(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove disk %s: %w", path, err)
	}
	return nil
}

// DiskPath returns the full filesystem path for a named disk image.
func (m *ImageManager) DiskPath(name string) string {
	return filepath.Join(m.imageDir, name+".qcow2")
}

// DiskExists reports whether a disk image with the given name exists.
func (m *ImageManager) DiskExists(name string) bool {
	_, err := os.Stat(m.DiskPath(name))
	return err == nil
}

// BuildFromDocker exports a Docker image filesystem and packages it as a qcow2 disk.
//
// The resulting image contains only the rootfs and is NOT directly bootable.
// For bootable VMs use a pre-built base image via CreateOverlay and run the
// user workload inside the VM with Docker-in-VM.
//
// Steps: docker create → docker export → virt-make-fs → cleanup.
func (m *ImageManager) BuildFromDocker(ctx context.Context, image, tag string, sizeGB int) (string, error) {
	if err := os.MkdirAll(m.imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	fullImage := image
	if tag != "" {
		fullImage += ":" + tag
	}

	containerName := "qudata-export-" + filepath.Base(image)

	// Pull the image.
	pull := exec.CommandContext(ctx, "docker", "pull", fullImage)
	if out, err := pull.CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker pull %s: %w: %s", fullImage, err, strings.TrimSpace(string(out)))
	}

	// Create a temporary container (not started).
	create := exec.CommandContext(ctx, "docker", "create", "--name", containerName, fullImage)
	if out, err := create.CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", containerName).Run()
	}()

	// Export the container filesystem to a tarball.
	tarPath := filepath.Join(m.imageDir, containerName+".tar")
	exportCmd := exec.CommandContext(ctx, "docker", "export", "-o", tarPath, containerName)
	if out, err := exportCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker export: %w: %s", err, strings.TrimSpace(string(out)))
	}
	defer os.Remove(tarPath)

	// Convert tarball to qcow2 using virt-make-fs.
	qcow2Path := filepath.Join(m.imageDir, containerName+".qcow2")
	mkfs := exec.CommandContext(ctx, "virt-make-fs",
		"--format=qcow2",
		"--type=ext4",
		fmt.Sprintf("--size=%dG", sizeGB),
		tarPath, qcow2Path)

	if out, err := mkfs.CombinedOutput(); err != nil {
		return "", fmt.Errorf("virt-make-fs: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return qcow2Path, nil
}
