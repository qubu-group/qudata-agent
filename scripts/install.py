#!/usr/bin/env python3
"""QuData Agent Installer - Single command installation with automatic IOMMU setup"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import textwrap
import urllib.request
from pathlib import Path

AGENT_NAME = "qudata-agent"
INSTALL_DIR = Path("/opt/qudata-agent")
BINARY_PATH = Path("/usr/local/bin") / AGENT_NAME
DATA_DIR = Path("/var/lib/qudata")
IMAGE_DIR = DATA_DIR / "images"
SSH_DIR = DATA_DIR / ".ssh"
LOG_DIR = Path("/var/log/qudata")
FRPC_DIR = Path("/etc/qudata")
FRPC_BINARY = Path("/usr/local/bin/frpc")
RUN_DIR = Path("/var/run/qudata")
SYSTEMD_UNIT = Path(f"/etc/systemd/system/{AGENT_NAME}.service")
STATE_FILE = DATA_DIR / "install_state.json"

REPO_URL = os.environ.get("REPO_URL", "https://github.com/qubu-group/qudata-agent.git")
GO_VERSION = "1.23.4"
FRP_VERSION = "0.61.1"

DEBIAN_CLOUD_IMAGE = "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2"
BASE_IMAGE_PATH = IMAGE_DIR / "qudata-base.qcow2"


def run(cmd, check=True, capture=True, **kw):
    r = subprocess.run(cmd, capture_output=capture, text=True, **kw)
    if check and r.returncode != 0:
        raise subprocess.CalledProcessError(r.returncode, cmd, r.stdout, r.stderr)
    return r


def apt_install(pkgs):
    env = {**os.environ, "DEBIAN_FRONTEND": "noninteractive"}
    run(["apt-get", "install", "-y", "--allow-downgrades"] + pkgs, env=env)


def save_state(state):
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    STATE_FILE.write_text(json.dumps(state, indent=2))


def load_state():
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {}


def detect_gpus():
    """Detect all NVIDIA GPUs and return their PCI addresses"""
    gpus = []
    r = run(["lspci", "-nn"], check=False)
    if r.returncode != 0:
        return gpus
    
    for line in r.stdout.splitlines():
        if "NVIDIA" in line and ("VGA" in line or "3D controller" in line):
            addr = line.split()[0]
            if not addr.startswith("0000:"):
                addr = f"0000:{addr}"
            
            match = re.search(r'\[([0-9a-f]{4}):([0-9a-f]{4})\]', line.lower())
            if match:
                gpus.append({
                    "addr": addr,
                    "vendor": match.group(1),
                    "device": match.group(2),
                    "name": line.split(":")[-1].strip().split("[")[0].strip()
                })
    return gpus


def detect_gpu_audio_devices(gpus):
    """Find NVIDIA audio devices in same IOMMU groups"""
    audio = []
    r = run(["lspci", "-nn"], check=False)
    if r.returncode != 0:
        return audio
    
    for line in r.stdout.splitlines():
        if "NVIDIA" in line and "Audio" in line:
            addr = line.split()[0]
            if not addr.startswith("0000:"):
                addr = f"0000:{addr}"
            
            match = re.search(r'\[([0-9a-f]{4}):([0-9a-f]{4})\]', line.lower())
            if match:
                audio.append({
                    "addr": addr,
                    "vendor": match.group(1),
                    "device": match.group(2)
                })
    return audio


def check_iommu_enabled():
    """Check if IOMMU is enabled in kernel"""
    r = run(["dmesg"], check=False)
    if "DMAR: IOMMU enabled" in r.stdout or "AMD-Vi" in r.stdout:
        return True
    
    if Path("/sys/kernel/iommu_groups/0").exists():
        return True
    
    return False


def configure_iommu(gpus, audio_devices):
    """Configure GRUB for IOMMU and VFIO"""
    print("\n-> Configuring IOMMU")
    
    grub_file = Path("/etc/default/grub")
    if not grub_file.exists():
        sys.exit("GRUB config not found")
    
    grub_content = grub_file.read_text()
    
    # Build VFIO IDs
    vfio_ids = set()
    for gpu in gpus:
        vfio_ids.add(f"{gpu['vendor']}:{gpu['device']}")
    for audio in audio_devices:
        vfio_ids.add(f"{audio['vendor']}:{audio['device']}")
    
    vfio_ids_str = ",".join(sorted(vfio_ids))
    
    # Determine CPU vendor
    cpu_vendor = "intel"
    with open("/proc/cpuinfo") as f:
        if "AMD" in f.read():
            cpu_vendor = "amd"
    
    iommu_param = "intel_iommu=on" if cpu_vendor == "intel" else "amd_iommu=on"
    new_params = f"{iommu_param} iommu=pt vfio-pci.ids={vfio_ids_str}"
    
    # Update GRUB_CMDLINE_LINUX
    pattern = r'GRUB_CMDLINE_LINUX="([^"]*)"'
    match = re.search(pattern, grub_content)
    
    if match:
        current = match.group(1)
        # Remove old iommu/vfio params
        current = re.sub(r'(intel_iommu|amd_iommu|iommu|vfio-pci\.ids)=[^\s]*\s*', '', current)
        current = current.strip()
        new_cmdline = f'{current} {new_params}'.strip()
        grub_content = re.sub(pattern, f'GRUB_CMDLINE_LINUX="{new_cmdline}"', grub_content)
    else:
        grub_content += f'\nGRUB_CMDLINE_LINUX="{new_params}"\n'
    
    grub_file.write_text(grub_content)
    print(f"  + GRUB updated: {new_params}")
    
    # Configure VFIO modules
    modules_conf = Path("/etc/modules-load.d/vfio.conf")
    modules_conf.write_text("vfio\nvfio_iommu_type1\nvfio_pci\n")
    
    # Blacklist nvidia at boot (will be loaded inside VM)
    blacklist = Path("/etc/modprobe.d/blacklist-nvidia.conf")
    blacklist.write_text("blacklist nouveau\nblacklist nvidia\nblacklist nvidia_drm\nblacklist nvidia_modeset\nblacklist nvidia_uvm\n")
    
    # VFIO PCI options
    vfio_conf = Path("/etc/modprobe.d/vfio.conf")
    vfio_conf.write_text(f"options vfio-pci ids={vfio_ids_str}\n")
    
    # Update initramfs and grub
    run(["update-initramfs", "-u"])
    run(["update-grub"])
    
    print("  + Initramfs and GRUB updated")
    return True


def download_base_image():
    """Download and customize Debian cloud image"""
    print("\n-> Preparing base image")
    
    IMAGE_DIR.mkdir(parents=True, exist_ok=True)
    
    if BASE_IMAGE_PATH.exists():
        print("  + Base image already exists")
        return
    
    print(f"  + Downloading Debian cloud image...")
    tmp_image = IMAGE_DIR / "debian-cloud.qcow2"
    urllib.request.urlretrieve(DEBIAN_CLOUD_IMAGE, tmp_image)
    
    print("  + Resizing to 20GB...")
    run(["qemu-img", "resize", str(tmp_image), "20G"])
    
    print("  + Customizing image...")
    customize_script = textwrap.dedent("""\
        #!/bin/bash
        set -e
        
        # Add non-free repos
        cat > /etc/apt/sources.list << 'EOF'
        deb http://deb.debian.org/debian bookworm main contrib non-free non-free-firmware
        deb http://deb.debian.org/debian bookworm-updates main contrib non-free non-free-firmware
        deb http://security.debian.org/debian-security bookworm-security main contrib non-free non-free-firmware
        EOF
        
        apt-get update
        apt-get install -y openssh-server curl wget gnupg ca-certificates
        
        # Install NVIDIA driver
        apt-get install -y nvidia-driver
        
        # Install Docker
        install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
        chmod a+r /etc/apt/keyrings/docker.asc
        echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list
        apt-get update
        apt-get install -y docker-ce docker-ce-cli containerd.io
        
        # Install NVIDIA Container Toolkit
        curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
        curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' > /etc/apt/sources.list.d/nvidia-container-toolkit.list
        apt-get update
        apt-get install -y nvidia-container-toolkit
        nvidia-ctk runtime configure --runtime=docker
        
        # Configure SSH
        mkdir -p /root/.ssh
        chmod 700 /root/.ssh
        sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
        sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
        systemctl enable ssh docker
        
        # Configure network
        cat > /etc/network/interfaces.d/eth0 << 'EOF'
        auto eth0
        iface eth0 inet dhcp
        EOF
        
        # Cleanup
        apt-get clean
        rm -rf /var/lib/apt/lists/*
    """)
    
    script_path = IMAGE_DIR / "customize.sh"
    script_path.write_text(customize_script)
    
    # Use virt-customize if available, otherwise use guestfish
    if shutil.which("virt-customize"):
        run(["virt-customize", "-a", str(tmp_image), "--run", str(script_path), "--selinux-relabel"])
    else:
        apt_install(["libguestfs-tools"])
        run(["virt-customize", "-a", str(tmp_image), "--run", str(script_path), "--selinux-relabel"])
    
    script_path.unlink()
    tmp_image.rename(BASE_IMAGE_PATH)
    print("  + Base image ready")


def inject_ssh_key():
    """Inject management SSH key into base image"""
    SSH_DIR.mkdir(parents=True, exist_ok=True)
    
    priv_key = SSH_DIR / "management_key"
    pub_key = SSH_DIR / "management_key.pub"
    
    if not priv_key.exists():
        print("  + Generating SSH key...")
        run(["ssh-keygen", "-t", "ed25519", "-f", str(priv_key), "-N", "", "-C", "qudata-management"])
    
    pub_key_content = pub_key.read_text().strip()
    
    print("  + Injecting SSH key into image...")
    run(["virt-customize", "-a", str(BASE_IMAGE_PATH),
         "--mkdir", "/root/.ssh",
         "--chmod", "0700:/root/.ssh",
         "--write", f"/root/.ssh/authorized_keys:{pub_key_content}",
         "--chmod", "0600:/root/.ssh/authorized_keys"])


def install_base():
    print("\n-> Installing base packages")
    run(["apt-get", "update"])
    apt_install(["curl", "wget", "ca-certificates", "gnupg", "lsb-release", "pciutils"])


def install_qemu():
    print("\n-> Installing QEMU")
    apt_install(["qemu-system-x86", "ovmf", "libguestfs-tools", "qemu-utils"])
    print("  + Installed")


def install_frpc():
    print("\n-> Installing FRPC")
    if FRPC_BINARY.exists():
        print("  + Already installed")
        return
    
    run(["bash", "-c",
         f'curl -fsSL "https://github.com/fatedier/frp/releases/download/v{FRP_VERSION}/'
         f'frp_{FRP_VERSION}_linux_amd64.tar.gz" | tar -xz'])
    run(["mv", f"frp_{FRP_VERSION}_linux_amd64/frpc", str(FRPC_BINARY)])
    run(["chmod", "+x", str(FRPC_BINARY)])
    run(["rm", "-rf", f"frp_{FRP_VERSION}_linux_amd64"])
    print(f"  + Installed v{FRP_VERSION}")


def build_agent(binary_path=None):
    if binary_path:
        print("\n-> Deploying binary")
        src = Path(binary_path)
        if not src.is_file():
            sys.exit(f"Binary not found: {binary_path}")
        shutil.copy2(src, BINARY_PATH)
        BINARY_PATH.chmod(0o755)
        print(f"  + {BINARY_PATH}")
        return
    
    print("\n-> Building agent")
    apt_install(["build-essential", "git"])
    
    if not shutil.which("go"):
        print("  + Installing Go...")
        tarball = f"go{GO_VERSION}.linux-amd64.tar.gz"
        run(["wget", "-q", f"https://go.dev/dl/{tarball}"])
        run(["rm", "-rf", "/usr/local/go"])
        run(["tar", "-C", "/usr/local", "-xzf", tarball])
        Path(tarball).unlink(missing_ok=True)
        os.environ["PATH"] = f"/usr/local/go/bin:{os.environ['PATH']}"
    
    if INSTALL_DIR.exists():
        run(["git", "pull", "-q"], cwd=str(INSTALL_DIR))
    else:
        run(["git", "clone", "-q", REPO_URL, str(INSTALL_DIR)])
    
    env = {**os.environ, "CGO_ENABLED": "1", "CGO_LDFLAGS": "-ldl"}
    run(["go", "build", "-o", str(BINARY_PATH), "./cmd/agent"], cwd=str(INSTALL_DIR), env=env)
    BINARY_PATH.chmod(0o755)
    print(f"  + {BINARY_PATH}")


def create_service(api_key, gpus, debug, service_url):
    print("\n-> Creating systemd service")
    
    for d in [DATA_DIR, IMAGE_DIR, SSH_DIR, LOG_DIR, FRPC_DIR, RUN_DIR]:
        d.mkdir(parents=True, exist_ok=True)
    
    gpu_addrs = ",".join(gpu["addr"] for gpu in gpus)
    
    env = [
        f'Environment="QUDATA_API_KEY={api_key}"',
        f'Environment="QUDATA_GPU_PCI_ADDRS={gpu_addrs}"',
        f'Environment="QUDATA_BASE_IMAGE={BASE_IMAGE_PATH}"',
        f'Environment="QUDATA_MANAGEMENT_KEY={SSH_DIR}/management_key"',
    ]
    if service_url:
        env.append(f'Environment="QUDATA_SERVICE_URL={service_url}"')
    if debug:
        env.append('Environment="QUDATA_DEBUG=true"')
    
    unit = textwrap.dedent(f"""\
        [Unit]
        Description=QuData Agent
        After=network.target

        [Service]
        Type=simple
        ExecStart={BINARY_PATH}
        Restart=always
        RestartSec=10
        {chr(10).join(env)}

        [Install]
        WantedBy=multi-user.target
    """)
    
    SYSTEMD_UNIT.write_text(unit)
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", AGENT_NAME])
    print(f"  + {SYSTEMD_UNIT}")


def start_service():
    print("\n-> Starting agent")
    run(["systemctl", "restart", AGENT_NAME])
    
    import time
    time.sleep(3)
    
    if run(["systemctl", "is-active", "--quiet", AGENT_NAME], check=False).returncode != 0:
        run(["journalctl", "-u", AGENT_NAME, "-n", "30", "--no-pager"], capture=False)
        sys.exit("Agent failed to start")
    
    print("  + Running")


def phase1(args):
    """Phase 1: Install packages, configure IOMMU, prepare reboot"""
    print("\n" + "=" * 50)
    print("  QuData Agent Installer - Phase 1")
    print("=" * 50)
    
    if args.debug:
        print("\n  ⚠️  DEBUG MODE - No GPU passthrough, mock data only")
        print("  ⚠️  This is for development/testing only!\n")
    
    gpus = detect_gpus()
    audio = detect_gpu_audio_devices(gpus)
    
    if not gpus and not args.debug:
        sys.exit("No NVIDIA GPU detected. Use --debug for testing without GPU.")
    
    if gpus:
        print(f"\n-> Found {len(gpus)} GPU(s):")
        for gpu in gpus:
            print(f"  + {gpu['addr']}: {gpu['name']}")
    
    install_base()
    install_qemu()
    install_frpc()
    build_agent(args.binary)
    
    if args.debug:
        create_service(args.api_key, [], True, args.service_url)
        download_base_image()
        inject_ssh_key()
        start_service()
        print_success()
        return
    
    iommu_ok = check_iommu_enabled()
    
    if not iommu_ok:
        configure_iommu(gpus, audio)
        
        save_state({
            "phase": 2,
            "api_key": args.api_key,
            "gpus": gpus,
            "service_url": args.service_url,
            "binary": args.binary
        })
        
        print("\n" + "=" * 50)
        print("  REBOOT REQUIRED")
        print("=" * 50)
        print("\n  IOMMU has been configured. System will reboot in 10 seconds.")
        print("  After reboot, installation will continue automatically.\n")
        
        # Create oneshot service to continue after reboot
        continue_unit = textwrap.dedent(f"""\
            [Unit]
            Description=QuData Agent Installer Phase 2
            After=network.target
            ConditionPathExists={STATE_FILE}

            [Service]
            Type=oneshot
            ExecStart=/usr/bin/python3 {INSTALL_DIR}/scripts/install.py --continue
            RemainAfterExit=yes

            [Install]
            WantedBy=multi-user.target
        """)
        
        Path("/etc/systemd/system/qudata-install-continue.service").write_text(continue_unit)
        run(["systemctl", "daemon-reload"])
        run(["systemctl", "enable", "qudata-install-continue.service"])
        
        import time
        time.sleep(10)
        run(["reboot"])
    else:
        phase2_continue(args.api_key, gpus, args.service_url)


def phase2_continue(api_key, gpus, service_url):
    """Phase 2: After reboot, finish installation"""
    print("\n" + "=" * 50)
    print("  QuData Agent Installer - Phase 2")
    print("=" * 50)
    
    # Disable continue service
    run(["systemctl", "disable", "qudata-install-continue.service"], check=False)
    Path("/etc/systemd/system/qudata-install-continue.service").unlink(missing_ok=True)
    
    if not check_iommu_enabled():
        sys.exit("IOMMU still not enabled after reboot. Check BIOS settings.")
    
    print("\n-> IOMMU is active")
    
    download_base_image()
    inject_ssh_key()
    create_service(api_key, gpus, False, service_url)
    start_service()
    
    STATE_FILE.unlink(missing_ok=True)
    print_success()


def print_success():
    print("\n" + "=" * 50)
    print("  Installation complete!")
    print("=" * 50)
    print(f"\n  Status:  systemctl status {AGENT_NAME}")
    print(f"  Logs:    journalctl -u {AGENT_NAME} -f")
    print()


def main():
    p = argparse.ArgumentParser(description="Install QuData Agent")
    p.add_argument("api_key", nargs="?", help="API key (ak-...)")
    p.add_argument("--binary", metavar="PATH", help="Pre-built binary")
    p.add_argument("--debug", action="store_true", help="Debug mode (no GPU)")
    p.add_argument("--service-url", metavar="URL", help="API URL override")
    p.add_argument("--continue", dest="cont", action="store_true", help="Continue after reboot")
    args = p.parse_args()
    
    if os.geteuid() != 0:
        sys.exit("Must run as root")
    
    if args.cont:
        state = load_state()
        if not state:
            sys.exit("No saved state found")
        phase2_continue(state["api_key"], state["gpus"], state.get("service_url"))
        return
    
    if not args.api_key:
        p.print_help()
        sys.exit(1)
    
    if not args.api_key.startswith("ak-"):
        sys.exit("Invalid API key format (must start with 'ak-')")
    
    try:
        phase1(args)
    except subprocess.CalledProcessError as e:
        print(f"\nFailed: {' '.join(str(x) for x in e.cmd)}", file=sys.stderr)
        if e.stderr:
            print(e.stderr, file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    main()
