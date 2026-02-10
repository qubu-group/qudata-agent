package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Build-time variables injected via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

// Config holds all agent configuration loaded from environment variables.
type Config struct {
	// APIKey is the Qudata API key (must start with "ak-").
	APIKey string

	// ServiceURL is the base URL of the Qudata API.
	ServiceURL string

	// Debug enables mock GPU data and verbose logging.
	Debug bool

	// DataDir is the root directory for persistent agent data.
	DataDir string

	// LogDir is the directory for log files.
	LogDir string

	// FRPCBinary is the path to the frpc executable.
	FRPCBinary string

	// FRPCConfigPath is the path where the generated frpc.toml is written.
	FRPCConfigPath string

	// Backend selects the virtualization backend: "docker" or "qemu".
	Backend string

	// QEMUBinary is the path to the qemu-system-x86_64 binary.
	QEMUBinary string

	// OVMFPath is the path to the OVMF UEFI firmware image.
	OVMFPath string

	// BaseImagePath is the path to the pre-built base qcow2 image for QEMU instances.
	BaseImagePath string

	// ImageDir is the directory for storing qcow2 disk images.
	ImageDir string

	// VMRunDir is the directory for QMP sockets and VM runtime files.
	VMRunDir string

	// GPUPCIAddr is the default PCI address of the GPU for VFIO passthrough.
	GPUPCIAddr string

	// ManagementKeyPath is the SSH private key used to manage QEMU guest instances.
	ManagementKeyPath string
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ServiceURL:     "https://internal.qudata.ai/v0",
		DataDir:        "/var/lib/qudata",
		LogDir:         "/var/log/qudata",
		FRPCBinary:     "/usr/local/bin/frpc",
		FRPCConfigPath: "/etc/qudata/frpc.toml",
		Backend:        "docker",
		QEMUBinary:     "/usr/bin/qemu-system-x86_64",
		OVMFPath:       "/usr/share/OVMF/OVMF_CODE.fd",
		ImageDir:       "/var/lib/qudata/images",
		VMRunDir:       "/var/run/qudata",
	}
}

// Load reads configuration from environment variables, applying defaults
// for anything not explicitly set. Returns an error if required values
// are missing or malformed.
func Load() (*Config, error) {
	cfg := DefaultConfig()

	cfg.APIKey = strings.TrimSpace(os.Getenv("QUDATA_API_KEY"))
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("QUDATA_API_KEY is required")
	}
	if !strings.HasPrefix(cfg.APIKey, "ak-") {
		return nil, fmt.Errorf("QUDATA_API_KEY must start with 'ak-'")
	}

	if v := os.Getenv("QUDATA_SERVICE_URL"); v != "" {
		cfg.ServiceURL = v
	}

	cfg.Debug = os.Getenv("QUDATA_AGENT_DEBUG") == "true"

	if v := os.Getenv("QUDATA_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	if v := os.Getenv("QUDATA_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}

	if v := os.Getenv("QUDATA_FRPC_BINARY"); v != "" {
		cfg.FRPCBinary = v
	}

	if v := os.Getenv("QUDATA_FRPC_CONFIG"); v != "" {
		cfg.FRPCConfigPath = v
	}

	if v := os.Getenv("QUDATA_BACKEND"); v != "" {
		cfg.Backend = v
	}

	if v := os.Getenv("QUDATA_QEMU_BINARY"); v != "" {
		cfg.QEMUBinary = v
	}

	if v := os.Getenv("QUDATA_OVMF_PATH"); v != "" {
		cfg.OVMFPath = v
	}

	if v := os.Getenv("QUDATA_BASE_IMAGE"); v != "" {
		cfg.BaseImagePath = v
	}

	if v := os.Getenv("QUDATA_IMAGE_DIR"); v != "" {
		cfg.ImageDir = v
	}

	if v := os.Getenv("QUDATA_VM_RUN_DIR"); v != "" {
		cfg.VMRunDir = v
	}

	if v := os.Getenv("QUDATA_GPU_PCI_ADDR"); v != "" {
		cfg.GPUPCIAddr = v
	}

	if v := os.Getenv("QUDATA_MANAGEMENT_KEY"); v != "" {
		cfg.ManagementKeyPath = v
	}

	return cfg, nil
}

// NewLogger creates a structured logger that writes to both stdout and a log file.
func NewLogger(cfg *Config, name string) (*slog.Logger, error) {
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	logPath := cfg.LogDir + "/" + name + ".log"
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", logPath, err)
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}

	handler := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)

	return logger, nil
}
