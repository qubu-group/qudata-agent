#!/usr/bin/env python3
"""
QuData Agent — Installation Script

Checks system dependencies, verifies compatibility, installs the agent,
and registers it as a systemd service.

Supports two modes:
  1. --binary /path/to/qudata-agent  — deploy a pre-built binary (no Go needed)
  2. Default                         — clone repo and compile from source

Usage:
    sudo python3 install.py <api-key> [--binary /path/to/binary] [--debug] [--service-url URL]
"""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import subprocess
import sys
import textwrap
from pathlib import Path
from typing import Dict, List, Optional

# ── Constants ──────────────────────────────────────────────────────────────

AGENT_NAME = "qudata-agent"
INSTALL_DIR = Path("/opt/qudata-agent")
BINARY_PATH = Path("/usr/local/bin") / AGENT_NAME
DATA_DIR = Path("/var/lib/qudata")
LOG_DIR = Path("/var/log/qudata")
FRPC_DIR = Path("/etc/qudata")
FRPC_BINARY = Path("/usr/local/bin/frpc")
SYSTEMD_UNIT = Path("/etc/systemd/system/{}.service".format(AGENT_NAME))
LOG_FILE = Path("/var/log/qudata-install.log")

REPO_URL = os.environ.get("REPO_URL", "https://github.com/qubu-group/qudata-agent.git")
GO_VERSION = "1.23.4"
FRP_VERSION = "0.61.1"

# ── Logging ────────────────────────────────────────────────────────────────

class Logger:
    def __init__(self, log_path):
        # type: (Path) -> None
        self.log_path = log_path
        log_path.parent.mkdir(parents=True, exist_ok=True)
        if log_path.exists():
            log_path.unlink()

    def info(self, msg):
        # type: (str) -> None
        print("  + {}".format(msg))
        self._write("[INFO] {}".format(msg))

    def step(self, msg):
        # type: (str) -> None
        print("\n-> {}".format(msg))
        self._write("[STEP] {}".format(msg))

    def warn(self, msg):
        # type: (str) -> None
        print("  ! {}".format(msg))
        self._write("[WARN] {}".format(msg))

    def error(self, msg):
        # type: (str) -> None
        print("  X {}".format(msg), file=sys.stderr)
        self._write("[ERROR] {}".format(msg))

    def _write(self, msg):
        # type: (str) -> None
        with open(str(self.log_path), "a") as f:
            f.write(msg + "\n")


log = Logger(LOG_FILE)

# ── Helpers ────────────────────────────────────────────────────────────────

def run(cmd, check=True, capture=True, **kwargs):
    # type: (List[str], bool, bool, **object) -> subprocess.CompletedProcess
    """Run a shell command, logging output to the install log."""
    log._write("$ {}".format(" ".join(cmd)))
    result = subprocess.run(
        cmd,
        capture_output=capture,
        text=True,
        **kwargs,
    )
    if result.stdout:
        log._write(result.stdout)
    if result.stderr:
        log._write(result.stderr)
    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, cmd, result.stdout, result.stderr)
    return result


def command_exists(name):
    # type: (str) -> bool
    return shutil.which(name) is not None


def apt_install(packages):
    # type: (List[str]) -> None
    """Install packages via apt-get."""
    env = dict(os.environ)
    env["DEBIAN_FRONTEND"] = "noninteractive"
    run(["apt-get", "install", "-y", "--allow-downgrades"] + packages, env=env)

# ── Checks ─────────────────────────────────────────────────────────────────

def check_root():
    # type: () -> None
    if os.geteuid() != 0:
        log.error("This script must be run as root (use sudo)")
        sys.exit(1)


def check_system():
    # type: () -> None
    log.step("Checking system compatibility")

    system = platform.system()
    if system != "Linux":
        log.error("Unsupported OS: {}. Only Linux is supported.".format(system))
        sys.exit(1)
    log.info("OS: {}".format(system))

    arch = platform.machine()
    if arch != "x86_64":
        log.error("Unsupported architecture: {}. Only x86_64 is supported.".format(arch))
        sys.exit(1)
    log.info("Architecture: {}".format(arch))

    kernel = platform.release()
    log.info("Kernel: {}".format(kernel))

    init_pid1 = Path("/run/systemd/system")
    if not init_pid1.exists():
        log.error("systemd is required but not detected")
        sys.exit(1)
    log.info("Init system: systemd")

    # Check available memory
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                if line.startswith("MemTotal:"):
                    kb = int(line.split()[1])
                    gb = kb / (1024 * 1024)
                    log.info("RAM: {:.1f} GB".format(gb))
                    if gb < 4:
                        log.warn("Less than 4 GB RAM")
                    break
    except Exception:
        log.warn("Could not determine available memory")

    # Check available disk space
    stat = os.statvfs("/")
    free_gb = (stat.f_bavail * stat.f_frsize) / (1024 ** 3)
    log.info("Free disk space: {:.1f} GB".format(free_gb))
    if free_gb < 20:
        log.warn("Less than 20 GB free disk space")


def check_gpu(debug):
    # type: (bool) -> bool
    """Check for NVIDIA GPU. Returns True if GPU is available."""
    log.step("Checking GPU")

    if debug:
        log.info("DEBUG mode -- skipping GPU check (will use mock data)")
        return False

    has_gpu = Path("/dev/nvidiactl").exists() and Path("/dev/nvidia0").exists()
    if not has_gpu:
        log.warn("No NVIDIA GPU detected")
        log.warn("Agent will run without real GPU metrics")
        return False

    if command_exists("nvidia-smi"):
        result = run(
            ["nvidia-smi", "--query-gpu=name,memory.total,driver_version",
             "--format=csv,noheader,nounits"],
            check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            parts = result.stdout.strip().split(", ")
            log.info("GPU: {}".format(parts[0]))
            if len(parts) > 1:
                log.info("VRAM: {} MiB".format(parts[1]))
            if len(parts) > 2:
                log.info("Driver: {}".format(parts[2]))
    else:
        log.warn("nvidia-smi not found")

    return True

# ── Installation Steps ─────────────────────────────────────────────────────

def install_base_packages():
    # type: () -> None
    log.step("Installing base system dependencies")
    run(["apt-get", "update"])
    apt_install(["curl", "wget", "ca-certificates", "gnupg", "lsb-release"])
    log.info("Base packages installed")


def install_docker():
    # type: () -> None
    log.step("Checking Docker")

    if command_exists("docker"):
        result = run(["docker", "--version"], check=False)
        if result.returncode == 0:
            log.info("Docker already installed: {}".format(result.stdout.strip()))
            return

    log.info("Installing Docker CE")
    run(["apt-get", "remove", "-y", "docker", "docker-engine", "docker.io",
         "containerd", "runc"], check=False)

    os.makedirs("/etc/apt/keyrings", exist_ok=True)

    run(["bash", "-c",
         "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | "
         "gpg --dearmor -o /etc/apt/keyrings/docker.gpg && "
         "chmod a+r /etc/apt/keyrings/docker.gpg"])

    arch = run(["dpkg", "--print-architecture"]).stdout.strip()
    codename = run(["lsb_release", "-cs"]).stdout.strip()
    repo_line = (
        "deb [arch={arch} signed-by=/etc/apt/keyrings/docker.gpg] "
        "https://download.docker.com/linux/ubuntu {codename} stable"
    ).format(arch=arch, codename=codename)
    Path("/etc/apt/sources.list.d/docker.list").write_text(repo_line + "\n")

    run(["apt-get", "update"])
    apt_install(["docker-ce", "docker-ce-cli", "containerd.io",
                 "docker-buildx-plugin", "docker-compose-plugin"])

    run(["systemctl", "enable", "docker"])
    run(["systemctl", "start", "docker"])
    log.info("Docker installed and started")


def install_nvidia_toolkit(has_gpu, debug):
    # type: (bool, bool) -> None
    if not has_gpu or debug:
        log.info("Skipping NVIDIA Container Toolkit")
        return

    log.step("Checking NVIDIA Container Toolkit")

    if command_exists("nvidia-ctk"):
        log.info("NVIDIA Container Toolkit already installed")
        return

    log.info("Installing NVIDIA Container Toolkit")

    run(["bash", "-c",
         "curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | "
         "gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg"])

    run(["bash", "-c",
         "curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | "
         "sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | "
         "tee /etc/apt/sources.list.d/nvidia-container-toolkit.list"])

    run(["apt-get", "update"])
    apt_install(["nvidia-container-toolkit"])

    run(["nvidia-ctk", "runtime", "configure", "--runtime=docker"])
    run(["systemctl", "restart", "docker"])
    log.info("NVIDIA Container Toolkit installed")


def install_frpc():
    # type: () -> None
    log.step("Checking FRPC")

    if FRPC_BINARY.exists():
        result = run([str(FRPC_BINARY), "--version"], check=False)
        if result.returncode == 0:
            log.info("FRPC already installed: {}".format(result.stdout.strip()))
            return

    log.info("Installing FRPC {}".format(FRP_VERSION))
    tarball_name = "frp_{}_linux_amd64".format(FRP_VERSION)
    run(["bash", "-c",
         'curl -fsSL "https://github.com/fatedier/frp/releases/download/v{ver}/'
         '{name}.tar.gz" | tar -xz'.format(ver=FRP_VERSION, name=tarball_name)])
    run(["mv", "{}/frpc".format(tarball_name), str(FRPC_BINARY)])
    run(["chmod", "+x", str(FRPC_BINARY)])
    run(["rm", "-rf", tarball_name])

    log.info("FRPC {} installed to {}".format(FRP_VERSION, FRPC_BINARY))


def deploy_prebuilt_binary(binary_path):
    # type: (str) -> None
    """Copy a pre-built binary to the install location."""
    log.step("Deploying pre-built binary")

    src = Path(binary_path)
    if not src.exists():
        log.error("Binary not found: {}".format(binary_path))
        sys.exit(1)
    if not src.is_file():
        log.error("Not a file: {}".format(binary_path))
        sys.exit(1)

    shutil.copy2(str(src), str(BINARY_PATH))
    BINARY_PATH.chmod(0o755)
    log.info("Binary deployed: {}".format(BINARY_PATH))


def build_from_source():
    # type: () -> None
    """Clone repo and compile the agent from source."""
    log.step("Installing build dependencies")
    apt_install(["build-essential", "git"])

    # Install Go if needed
    if not command_exists("go"):
        log.info("Installing Go {}".format(GO_VERSION))
        tarball = "go{}.linux-amd64.tar.gz".format(GO_VERSION)
        run(["wget", "-q", "https://go.dev/dl/{}".format(tarball)])
        run(["rm", "-rf", "/usr/local/go"])
        run(["tar", "-C", "/usr/local", "-xzf", tarball])
        Path(tarball).unlink(missing_ok=True)
        os.environ["PATH"] = "/usr/local/go/bin:" + os.environ["PATH"]

        profile = Path("/etc/profile")
        marker = "/usr/local/go/bin"
        if marker not in profile.read_text():
            with open(str(profile), "a") as f:
                f.write("\nexport PATH=$PATH:/usr/local/go/bin\n")

    log.step("Building agent from source")

    if INSTALL_DIR.exists():
        log.info("Updating repository")
        run(["git", "pull", "-q"], cwd=str(INSTALL_DIR))
    else:
        log.info("Cloning repository")
        run(["git", "clone", "-q", REPO_URL, str(INSTALL_DIR)])

    log.info("Compiling agent binary")
    env = dict(os.environ)
    env["CGO_ENABLED"] = "1"
    env["CGO_LDFLAGS"] = "-ldl"

    run(
        ["go", "build", "-o", str(BINARY_PATH), "./cmd/agent"],
        cwd=str(INSTALL_DIR),
        env=env,
    )
    BINARY_PATH.chmod(0o755)
    log.info("Agent built: {}".format(BINARY_PATH))


def create_directories():
    # type: () -> None
    for d in [DATA_DIR, DATA_DIR / "data", LOG_DIR, FRPC_DIR]:
        d.mkdir(parents=True, exist_ok=True)


def create_systemd_service(api_key, debug, service_url):
    # type: (str, bool, Optional[str]) -> None
    log.step("Configuring systemd service")

    env_lines = [
        'Environment="QUDATA_API_KEY={}"'.format(api_key),
    ]
    if service_url:
        env_lines.append('Environment="QUDATA_SERVICE_URL={}"'.format(service_url))
    if debug:
        env_lines.append('Environment="QUDATA_AGENT_DEBUG=true"')

    env_block = "\n".join(env_lines)

    unit = textwrap.dedent("""\
        [Unit]
        Description=QuData Agent
        After=network.target docker.service
        Requires=docker.service

        [Service]
        Type=simple
        User=root
        ExecStart={binary}
        Restart=always
        RestartSec=10
        StandardOutput=journal
        StandardError=journal
        SyslogIdentifier={name}
        {env}

        [Install]
        WantedBy=multi-user.target
    """).format(binary=BINARY_PATH, name=AGENT_NAME, env=env_block)

    SYSTEMD_UNIT.write_text(unit)
    log.info("Systemd unit created: {}".format(SYSTEMD_UNIT))


def start_service():
    # type: () -> None
    log.step("Starting agent service")

    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", AGENT_NAME])
    run(["systemctl", "restart", AGENT_NAME])

    import time
    time.sleep(3)

    result = run(["systemctl", "is-active", "--quiet", AGENT_NAME], check=False)
    if result.returncode != 0:
        log.error("Agent failed to start!")
        run(["journalctl", "-u", AGENT_NAME, "-n", "20", "--no-pager"], check=False, capture=False)
        sys.exit(1)

    log.info("Agent is running")

# ── Main ───────────────────────────────────────────────────────────────────

def main():
    # type: () -> None
    parser = argparse.ArgumentParser(
        description="Install the QuData Agent",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("api_key", help="Qudata API key (must start with 'ak-')")
    parser.add_argument("--binary", metavar="PATH",
                        help="Path to pre-built agent binary (skip compilation)")
    parser.add_argument("--debug", action="store_true",
                        help="Enable debug mode (mock GPU)")
    parser.add_argument("--service-url", metavar="URL",
                        help="Override Qudata API URL")
    args = parser.parse_args()

    if not args.api_key.startswith("ak-"):
        log.error("Invalid API key format (must start with 'ak-')")
        sys.exit(1)

    print("\n" + "=" * 60)
    print("  QuData Agent Installer")
    print("=" * 60)

    if args.binary:
        print("  Mode: deploy pre-built binary")
    else:
        print("  Mode: build from source")

    try:
        check_root()
        check_system()
        has_gpu = check_gpu(args.debug)

        install_base_packages()
        install_docker()
        install_nvidia_toolkit(has_gpu, args.debug)
        install_frpc()
        create_directories()

        if args.binary:
            deploy_prebuilt_binary(args.binary)
        else:
            build_from_source()

        create_systemd_service(args.api_key, args.debug, args.service_url)
        start_service()

        print("\n" + "=" * 60)
        print("  + Installation complete!")
        print("=" * 60)
        print("\n  Useful commands:")
        print("    Status:  systemctl status {}".format(AGENT_NAME))
        print("    Logs:    journalctl -u {} -f".format(AGENT_NAME))
        print("    Stop:    systemctl stop {}".format(AGENT_NAME))
        print("    Restart: systemctl restart {}".format(AGENT_NAME))
        print()

        LOG_FILE.unlink(missing_ok=True)

    except subprocess.CalledProcessError as e:
        log.error("Command failed: {}".format(" ".join(str(x) for x in e.cmd)))
        if e.stderr:
            log.error("stderr: {}".format(str(e.stderr)[:500]))
        print("\nInstallation failed. Full log: {}".format(LOG_FILE))
        sys.exit(1)
    except KeyboardInterrupt:
        log.error("Installation interrupted by user")
        sys.exit(130)
    except Exception as e:
        log.error("Unexpected error: {}".format(e))
        print("\nInstallation failed. Full log: {}".format(LOG_FILE))
        sys.exit(1)


if __name__ == "__main__":
    main()
