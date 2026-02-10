package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

type Config struct {
	APIKey     string
	ServiceURL string
	Debug      bool
	DataDir    string
	LogDir     string

	FRPCBinary     string
	FRPCConfigPath string

	QEMUBinary        string
	OVMFCodePath      string
	OVMFVarsPath      string
	BaseImagePath     string
	ImageDir          string
	VMRunDir          string
	GPUPCIAddr        string
	ManagementKeyPath string

	VMDefaultCPUs   string
	VMDefaultMemory string
	VMDiskSizeGB    int
}

func DefaultConfig() *Config {
	code, vars := findOVMF()
	return &Config{
		ServiceURL:      "https://internal.qudata.ai/v0",
		DataDir:         "/var/lib/qudata",
		LogDir:          "/var/log/qudata",
		FRPCBinary:      "/usr/local/bin/frpc",
		FRPCConfigPath:  "/etc/qudata/frpc.toml",
		QEMUBinary:      "/usr/bin/qemu-system-x86_64",
		OVMFCodePath:    code,
		OVMFVarsPath:    vars,
		ImageDir:        "/var/lib/qudata/images",
		VMRunDir:        "/var/run/qudata",
		VMDefaultCPUs:   "4",
		VMDefaultMemory: "8G",
		VMDiskSizeGB:    50,
	}
}

func findOVMF() (code, vars string) {
	pairs := [][2]string{
		{"/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"},
		{"/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"},
		{"/usr/share/edk2/ovmf/OVMF_CODE.fd", "/usr/share/edk2/ovmf/OVMF_VARS.fd"},
	}
	for _, p := range pairs {
		if _, e1 := os.Stat(p[0]); e1 == nil {
			if _, e2 := os.Stat(p[1]); e2 == nil {
				return p[0], p[1]
			}
		}
	}
	return "/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"
}

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
	if v := os.Getenv("QUDATA_QEMU_BINARY"); v != "" {
		cfg.QEMUBinary = v
	}
	if v := os.Getenv("QUDATA_OVMF_CODE"); v != "" {
		cfg.OVMFCodePath = v
	}
	if v := os.Getenv("QUDATA_OVMF_VARS"); v != "" {
		cfg.OVMFVarsPath = v
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
		cfg.GPUPCIAddr = strings.TrimSpace(v)
	}
	if v := os.Getenv("QUDATA_GPU_PCI_ADDRS"); v != "" && cfg.GPUPCIAddr == "" {
		addrs := strings.Split(v, ",")
		if len(addrs) > 0 && strings.TrimSpace(addrs[0]) != "" {
			cfg.GPUPCIAddr = strings.TrimSpace(addrs[0])
		}
	}
	if v := os.Getenv("QUDATA_MANAGEMENT_KEY"); v != "" {
		cfg.ManagementKeyPath = v
	}
	if v := os.Getenv("QUDATA_VM_CPUS"); v != "" {
		cfg.VMDefaultCPUs = v
	}
	if v := os.Getenv("QUDATA_VM_MEMORY"); v != "" {
		cfg.VMDefaultMemory = v
	}

	cfg.Debug = os.Getenv("QUDATA_DEBUG") == "true"

	return cfg, nil
}

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
	return slog.New(handler), nil
}
