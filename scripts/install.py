#!/usr/bin/env python3
"""QuData Agent Installer — single-command setup with IOMMU and VFIO."""

import argparse
import json
import os
import re
import select
import shutil
import subprocess
import sys
import textwrap
import time
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
CONTINUE_UNIT = Path("/etc/systemd/system/qudata-install-continue.service")
STATE_FILE = DATA_DIR / "install_state.json"
GPU_INFO_PATH = DATA_DIR / "gpu-info.json"

REPO_URL = os.environ.get(
    "REPO_URL", "https://github.com/qubu-group/qudata-agent.git"
)
GO_VERSION = "1.23.4"
FRP_VERSION = "0.61.1"

UBUNTU_CLOUD_IMAGE = (
    "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
)
BASE_IMAGE_PATH = IMAGE_DIR / "qudata-base.qcow2"


# libguestfs: use direct backend (no libvirt dependency).
os.environ["LIBGUESTFS_BACKEND"] = "direct"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def run(cmd, check=True, capture=True, **kw):
    r = subprocess.run(cmd, capture_output=capture, text=True, **kw)
    if check and r.returncode != 0:
        raise subprocess.CalledProcessError(r.returncode, cmd, r.stdout, r.stderr)
    return r


def apt_install(pkgs):
    env = {**os.environ, "DEBIAN_FRONTEND": "noninteractive"}
    run(["apt-get", "install", "-y", "--allow-downgrades"] + pkgs, env=env)


def get_distro_id():
    """Detect the distribution ID (debian, ubuntu, etc.)."""
    try:
        with open("/etc/os-release") as f:
            for line in f:
                if line.startswith("ID="):
                    return line.strip().split("=")[1].strip('"').lower()
    except FileNotFoundError:
        pass
    return "unknown"


def get_kernel_package():
    """Return the appropriate kernel package name for libguestfs."""
    distro = get_distro_id()
    if distro == "ubuntu":
        return "linux-image-generic"
    elif distro == "debian":
        return "linux-image-amd64"
    else:
        # For other distros, try generic first, fall back to nothing
        # as kernel should already be installed
        return None


def save_state(state):
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    STATE_FILE.write_text(json.dumps(state, indent=2))


def load_state():
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {}


# ---------------------------------------------------------------------------
# GPU detection
# ---------------------------------------------------------------------------

def detect_gpus():
    gpus = []
    r = run(["lspci", "-nn"], check=False)
    if r.returncode != 0:
        return gpus
    for line in r.stdout.splitlines():
        if "NVIDIA" in line and ("VGA" in line or "3D controller" in line):
            addr = line.split()[0]
            if not addr.startswith("0000:"):
                addr = f"0000:{addr}"
            id_match = re.search(r"\[([0-9a-f]{4}):([0-9a-f]{4})\]", line.lower())
            if id_match:
                # lspci format: "ADDR CLASS [CODE]: VENDOR DEVICE [VVVV:DDDD] (rev XX)"
                # Name = everything after first ": " up to "[VVVV:DDDD]"
                parts = line.split(": ", 1)
                name = "Unknown GPU"
                if len(parts) > 1:
                    name = re.sub(
                        r"\s*\[[0-9a-f]{4}:[0-9a-f]{4}\].*$", "",
                        parts[1], flags=re.IGNORECASE,
                    ).strip()
                gpus.append({
                    "addr": addr,
                    "vendor": id_match.group(1),
                    "device": id_match.group(2),
                    "name": name,
                })
    return gpus


def detect_gpu_audio_devices(gpus):
    audio = []
    r = run(["lspci", "-nn"], check=False)
    if r.returncode != 0:
        return audio
    for line in r.stdout.splitlines():
        if "NVIDIA" in line and "Audio" in line:
            addr = line.split()[0]
            if not addr.startswith("0000:"):
                addr = f"0000:{addr}"
            match = re.search(r"\[([0-9a-f]{4}):([0-9a-f]{4})\]", line.lower())
            if match:
                audio.append(
                    {
                        "addr": addr,
                        "vendor": match.group(1),
                        "device": match.group(2),
                    }
                )
    return audio


# ---------------------------------------------------------------------------
# IOMMU / VFIO
# ---------------------------------------------------------------------------

def check_iommu_enabled():
    r = run(["dmesg"], check=False)
    if "DMAR: IOMMU enabled" in r.stdout or "AMD-Vi" in r.stdout:
        return True
    return Path("/sys/kernel/iommu_groups/0").exists()


def configure_iommu(gpus, audio_devices):
    print("\n-> Configuring IOMMU")

    grub_file = Path("/etc/default/grub")
    if not grub_file.exists():
        sys.exit("GRUB config not found")

    grub_content = grub_file.read_text()

    vfio_ids = set()
    for gpu in gpus:
        vfio_ids.add(f"{gpu['vendor']}:{gpu['device']}")
    for audio in audio_devices:
        vfio_ids.add(f"{audio['vendor']}:{audio['device']}")
    vfio_ids_str = ",".join(sorted(vfio_ids))

    cpu_vendor = "intel"
    with open("/proc/cpuinfo") as f:
        if "AMD" in f.read():
            cpu_vendor = "amd"

    iommu_param = (
        "intel_iommu=on" if cpu_vendor == "intel" else "amd_iommu=on"
    )
    new_params = f"{iommu_param} iommu=pt vfio-pci.ids={vfio_ids_str}"

    pattern = r'GRUB_CMDLINE_LINUX="([^"]*)"'
    match = re.search(pattern, grub_content)
    if match:
        current = match.group(1)
        current = re.sub(
            r"(intel_iommu|amd_iommu|iommu|vfio-pci\.ids)=[^\s]*\s*",
            "",
            current,
        )
        new_cmdline = f"{current.strip()} {new_params}".strip()
        grub_content = re.sub(
            pattern, f'GRUB_CMDLINE_LINUX="{new_cmdline}"', grub_content
        )
    else:
        grub_content += f'\nGRUB_CMDLINE_LINUX="{new_params}"\n'

    grub_file.write_text(grub_content)
    print(f"  + GRUB: {new_params}")

    Path("/etc/modules-load.d/vfio.conf").write_text(
        "vfio\nvfio_iommu_type1\nvfio_pci\n"
    )
    Path("/etc/modprobe.d/blacklist-nvidia.conf").write_text(
        "blacklist nouveau\nblacklist nvidia\nblacklist nvidia_drm\n"
        "blacklist nvidia_modeset\nblacklist nvidia_uvm\n"
    )
    Path("/etc/modprobe.d/vfio.conf").write_text(
        f"options vfio-pci ids={vfio_ids_str}\n"
    )

    run(["update-initramfs", "-u"])
    run(["update-grub"])

    run(["systemctl", "stop", "nvidia-persistenced.service"], check=False)
    run(["systemctl", "disable", "nvidia-persistenced.service"], check=False)

    print("  + initramfs and GRUB updated")
    return True


# ---------------------------------------------------------------------------
# SSH key
# ---------------------------------------------------------------------------

def generate_ssh_key():
    SSH_DIR.mkdir(parents=True, exist_ok=True)
    priv_key = SSH_DIR / "management_key"
    pub_key = SSH_DIR / "management_key.pub"
    if not priv_key.exists():
        print("  + Generating SSH key")
        run(
            [
                "ssh-keygen",
                "-t",
                "ed25519",
                "-f",
                str(priv_key),
                "-N",
                "",
                "-C",
                "qudata-management",
            ]
        )
    return pub_key.read_text().strip()


# ---------------------------------------------------------------------------
# Base image (Ubuntu + NVIDIA driver, no Docker)
# ---------------------------------------------------------------------------

def create_cloud_init_iso(ssh_pubkey):
    ci_dir = IMAGE_DIR / "cloud-init"
    ci_dir.mkdir(parents=True, exist_ok=True)

    (ci_dir / "meta-data").write_text(
        "instance-id: qudata-customize\nlocal-hostname: qudata-base\n"
    )

    user_data = textwrap.dedent(
        f"""\
        #cloud-config

        ssh_authorized_keys:
          - {ssh_pubkey}

        runcmd:
          - export DEBIAN_FRONTEND=noninteractive
          - echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections

          - |
            for i in $(seq 1 30); do
              ping -c1 8.8.8.8 >/dev/null 2>&1 && break
              sleep 2
            done

          - apt-get update
          - apt-get install -y openssh-server curl wget gnupg ca-certificates

          - apt-get install -y nvidia-driver-560 || apt-get install -y nvidia-driver || true

          - mkdir -p /root/.ssh
          - chmod 700 /root/.ssh
          - sed -i 's/^#*PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
          - sed -i 's/^#*PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
          - systemctl enable ssh

          - |
            cat > /etc/systemd/network/99-dhcp-all.network << 'NETEOF'
            [Match]
            Name=en*

            [Network]
            DHCP=yes
            NETEOF
          - systemctl enable systemd-networkd

          - apt-get clean
          - rm -rf /var/lib/apt/lists/*

          - systemctl disable cloud-init cloud-init-local cloud-config cloud-final 2>/dev/null || true
          - touch /etc/cloud/cloud-init.disabled
          - cloud-init clean --logs

          - touch /var/lib/cloud/instance/customization-complete
          - poweroff
    """
    )
    (ci_dir / "user-data").write_text(user_data)

    iso_path = IMAGE_DIR / "cloud-init.iso"
    run(
        [
            "genisoimage",
            "-output",
            str(iso_path),
            "-volid",
            "cidata",
            "-joliet",
            "-rock",
            str(ci_dir / "user-data"),
            str(ci_dir / "meta-data"),
        ]
    )
    shutil.rmtree(ci_dir)
    return iso_path


def download_base_image():
    print("\n-> Preparing base image (Ubuntu)")

    IMAGE_DIR.mkdir(parents=True, exist_ok=True)

    if BASE_IMAGE_PATH.exists():
        print("  + Base image already exists")
        return

    ssh_pubkey = generate_ssh_key()

    print("  + Downloading Ubuntu cloud image")
    tmp_image = IMAGE_DIR / "ubuntu-cloud.qcow2"
    urllib.request.urlretrieve(UBUNTU_CLOUD_IMAGE, tmp_image)

    print("  + Resizing to 20 GB")
    run(["qemu-img", "resize", str(tmp_image), "20G"])

    if not shutil.which("genisoimage"):
        apt_install(["genisoimage"])

    print("  + Creating cloud-init config")
    ci_iso = create_cloud_init_iso(ssh_pubkey)

    print("  + Starting VM for customisation (may take 10-15 min)")

    qemu_cmd = [
        "qemu-system-x86_64",
        "-machine",
        "q35,accel=kvm",
        "-cpu",
        "host",
        "-m",
        "4096",
        "-smp",
        "4",
        "-drive",
        f"file={tmp_image},format=qcow2,if=virtio",
        "-drive",
        f"file={ci_iso},format=raw,if=virtio,readonly=on",
        "-netdev",
        "user,id=net0,hostfwd=tcp:127.0.0.1:2222-:22",
        "-device",
        "virtio-net-pci,netdev=net0",
        "-nographic",
        "-serial",
        "mon:stdio",
    ]

    proc = subprocess.Popen(
        qemu_cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )

    start_time = time.time()
    timeout = 20 * 60

    try:
        while proc.poll() is None:
            if time.time() - start_time > timeout:
                proc.terminate()
                proc.wait(timeout=10)
                raise TimeoutError("VM customisation timed out")

            if select.select([proc.stdout], [], [], 1.0)[0]:
                line = proc.stdout.readline()
                if line:
                    print(f"    {line.rstrip()}")

        for line in proc.stdout:
            print(f"    {line.rstrip()}")

        if proc.returncode != 0:
            raise subprocess.CalledProcessError(proc.returncode, qemu_cmd)
    except KeyboardInterrupt:
        proc.terminate()
        proc.wait(timeout=10)
        raise
    finally:
        ci_iso.unlink(missing_ok=True)

    print("  + Verifying customisation")

    if not shutil.which("virt-cat"):
        apt_install(["libguestfs-tools"])

    check = run(
        [
            "virt-cat",
            "-a",
            str(tmp_image),
            "/var/lib/cloud/instance/customization-complete",
        ],
        check=False,
    )
    if check.returncode != 0:
        print("  ! Warning: customisation may not have completed")

    print("  + Cleaning cloud-init state")
    run(
        [
            "virt-customize",
            "-a",
            str(tmp_image),
            "--delete",
            "/var/lib/cloud/instance/customization-complete",
            "--run-command",
            "rm -rf /var/lib/cloud/instances/*",
            "--run-command",
            "rm -f /etc/machine-id",
            "--run-command",
            "truncate -s 0 /etc/machine-id",
        ],
        check=False,
    )

    tmp_image.rename(BASE_IMAGE_PATH)
    print("  + Base image ready")


def stop_running_agent():
    """Stop agent and kill any QEMU processes to release the base image."""
    run(["systemctl", "stop", AGENT_NAME], check=False)
    # Kill leftover QEMU instances that may lock the base image.
    r = run(["pgrep", "-f", "qemu-system"], check=False)
    if r.returncode == 0:
        run(["pkill", "-9", "-f", "qemu-system"], check=False)
        time.sleep(1)


def prepare_base_image():
    """Inject management SSH key, fix networking, harden SSH, disable cloud-init."""
    if not (SSH_DIR / "management_key").exists():
        generate_ssh_key()

    pub_key_content = (SSH_DIR / "management_key.pub").read_text().strip()
    print("  + Configuring base image")

    run([
        "virt-customize", "-a", str(BASE_IMAGE_PATH),

        # SSH key
        "--mkdir", "/root/.ssh",
        "--chmod", "0700:/root/.ssh",
        "--write", f"/root/.ssh/authorized_keys:{pub_key_content}",
        "--chmod", "0600:/root/.ssh/authorized_keys",

        # Network: clean old configs, force systemd-networkd with wildcard DHCP
        "--run-command",
        "rm -f /etc/network/interfaces.d/* /etc/netplan/*.yaml "
        "/etc/systemd/network/[0-8]*.network 2>/dev/null; "
        "printf 'auto lo\\niface lo inet loopback\\n' > /etc/network/interfaces; "
        "mkdir -p /etc/systemd/network && "
        "printf '[Match]\\nName=en*\\n\\n[Network]\\nDHCP=yes\\n' "
        "> /etc/systemd/network/99-dhcp-all.network; "
        "systemctl enable systemd-networkd 2>/dev/null; "
        "systemctl disable networking 2>/dev/null; "
        "mkdir -p /etc/systemd/system/systemd-networkd-wait-online.service.d && "
        "printf '[Service]\\nExecStart=\\nExecStart=/lib/systemd/systemd-networkd-wait-online --any --timeout=10\\n' "
        "> /etc/systemd/system/systemd-networkd-wait-online.service.d/any.conf; "
        "true",

        # SSH: allow root login, disable locale forwarding (prevents LC_CTYPE warnings)
        "--run-command",
        "sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config; "
        "sed -i 's/^#*PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config; "
        "sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config; "
        "sed -i 's/^AcceptEnv.*/#AcceptEnv/' /etc/ssh/sshd_config; "
        "systemctl enable ssh 2>/dev/null; systemctl enable sshd 2>/dev/null; true",

        # Set sane default locale
        "--run-command",
        "echo 'LANG=en_US.UTF-8' > /etc/default/locale; "
        "echo 'en_US.UTF-8 UTF-8' >> /etc/locale.gen; "
        "locale-gen en_US.UTF-8 2>/dev/null; true",

        # Disable cloud-init
        "--run-command",
        "systemctl disable cloud-init cloud-init-local cloud-config cloud-final 2>/dev/null; "
        "touch /etc/cloud/cloud-init.disabled; true",

        # Disable noisy services for faster boot
        "--run-command",
        "systemctl disable apt-daily.timer apt-daily-upgrade.timer man-db.timer "
        "fstrim.timer e2scrub_all.timer unattended-upgrades.service docker.service "
        "docker.socket containerd.service 2>/dev/null; "
        "systemctl mask snapd.service snapd.socket 2>/dev/null; true",

        # Suppress cloud-init locale check
        "--run-command",
        "touch /var/lib/cloud/instance/locale-check.skip; true",
    ])

    # Verify
    v = run(
        ["virt-cat", "-a", str(BASE_IMAGE_PATH),
         "/etc/systemd/network/99-dhcp-all.network"],
        check=False,
    )
    if "Name=en*" in v.stdout:
        print("  + Network: systemd-networkd wildcard DHCP")
    else:
        print("  ! WARNING: network config may not be correct")


# ---------------------------------------------------------------------------
# Test GPU passthrough
# ---------------------------------------------------------------------------

def _find_ovmf():
    """Find OVMF firmware pair (code + vars). Returns (code, vars) or (None, None)."""
    pairs = [
        ("/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"),
        ("/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"),
        ("/usr/share/edk2/ovmf/OVMF_CODE.fd", "/usr/share/edk2/ovmf/OVMF_VARS.fd"),
    ]
    for code, vars_tmpl in pairs:
        if Path(code).exists() and Path(vars_tmpl).exists():
            return code, vars_tmpl
    return None, None


def _show_log_tail(log_path, lines=30):
    """Print last N lines of a log file."""
    try:
        content = log_path.read_text().strip()
        if not content:
            print("    (log empty)")
            return
        for line in content.split("\n")[-lines:]:
            print(f"    {line}")
    except Exception:
        print("    (could not read log)")


def _ssh_cmd(port, key_path, remote_cmd):
    """Build SSH command list."""
    return [
        "ssh",
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=/dev/null",
        "-o", "ConnectTimeout=2",
        "-o", "BatchMode=yes",
        "-o", "LogLevel=ERROR",
        "-p", str(port),
        "-i", str(key_path),
        "root@127.0.0.1",
        remote_cmd,
    ]


def _save_gpu_info(ssh_port, ssh_key):
    """Query full GPU info from VM and save to JSON for agent use."""
    try:
        # name, memory (MiB), driver, cuda version
        r = run(
            _ssh_cmd(ssh_port, ssh_key,
                     "nvidia-smi --query-gpu=name,memory.total,driver_version "
                     "--format=csv,noheader,nounits"),
            check=False,
        )
        if r.returncode != 0 or not r.stdout.strip():
            return

        # Parse: "Tesla T4, 15360, 535.261.03"
        parts = [p.strip() for p in r.stdout.strip().splitlines()[0].split(",")]
        if len(parts) < 3:
            return
        name = parts[0]
        vram_mib = float(parts[1])
        driver = parts[2]

        # Get CUDA version from nvidia-smi header
        cuda_ver = 0.0
        r2 = run(
            _ssh_cmd(ssh_port, ssh_key, "nvidia-smi"),
            check=False,
        )
        if r2.returncode == 0:
            for line in r2.stdout.splitlines():
                if "CUDA Version" in line:
                    m = re.search(r"CUDA Version:\s*([0-9.]+)", line)
                    if m:
                        cuda_ver = float(m.group(1))
                    break

        # Count GPUs
        r3 = run(
            _ssh_cmd(ssh_port, ssh_key,
                     "nvidia-smi --query-gpu=name --format=csv,noheader | wc -l"),
            check=False,
        )
        count = 1
        if r3.returncode == 0 and r3.stdout.strip().isdigit():
            count = int(r3.stdout.strip())

        info = {
            "name": name,
            "count": count,
            "vram_gb": round(vram_mib / 1024, 1),
            "max_cuda": cuda_ver,
            "driver_version": driver,
        }

        DATA_DIR.mkdir(parents=True, exist_ok=True)
        GPU_INFO_PATH.write_text(json.dumps(info, indent=2))
        print(f"  + GPU info saved: {info['name']}, {info['vram_gb']} GB, CUDA {cuda_ver}")

    except Exception as e:
        print(f"  ! Warning: could not save GPU info: {e}")


def test_gpu_passthrough(gpu_addr):
    """Boot a test VM with GPU passthrough, verify nvidia-smi, tear down."""
    print("\n-> Testing GPU passthrough")
    print(f"  + GPU: {gpu_addr}")

    ssh_key = SSH_DIR / "management_key"
    ssh_port = 2299
    RUN_DIR.mkdir(parents=True, exist_ok=True)
    qemu_log = RUN_DIR / "gpu-test.log"

    # ---- Pre-flight checks ----

    if not ssh_key.exists():
        print(f"  ! SSH key missing: {ssh_key}")
        return False

    # Check VFIO driver
    driver_link = Path(f"/sys/bus/pci/devices/{gpu_addr}/driver")
    if not driver_link.exists():
        print(f"  ! No driver bound to {gpu_addr}")
        return False
    drv = os.path.basename(os.readlink(str(driver_link)))
    if drv != "vfio-pci":
        print(f"  ! Driver is '{drv}', expected 'vfio-pci'")
        return False
    print(f"  + Driver: vfio-pci")

    # Check VFIO group device
    iommu_link = Path(f"/sys/bus/pci/devices/{gpu_addr}/iommu_group")
    if iommu_link.exists():
        group = os.path.basename(os.readlink(str(iommu_link)))
        vfio_dev = Path(f"/dev/vfio/{group}")
        if not vfio_dev.exists():
            print(f"  ! /dev/vfio/{group} missing")
            return False
        print(f"  + VFIO: /dev/vfio/{group}")

    # Find OVMF (required for GPU ROM initialization)
    ovmf_code, ovmf_vars_tmpl = _find_ovmf()
    if not ovmf_code:
        print("  ! OVMF firmware not found")
        print("    GPU passthrough requires UEFI: apt-get install ovmf")
        return False
    print(f"  + OVMF: {ovmf_code}")

    # ---- Prepare artifacts ----

    overlay = IMAGE_DIR / "gpu-test.qcow2"
    ovmf_vars = RUN_DIR / "gpu-test-VARS.fd"
    cleanup_files = [overlay, ovmf_vars]

    try:
        # Overlay disk
        run([
            "qemu-img", "create", "-f", "qcow2",
            "-b", str(BASE_IMAGE_PATH), "-F", "qcow2", str(overlay),
        ])

        # OVMF vars copy (UEFI needs writable variable store)
        shutil.copy2(ovmf_vars_tmpl, ovmf_vars)

        # ---- QEMU command ----

        qemu_cmd = [
            "qemu-system-x86_64",
            "-machine", "q35,accel=kvm",
            "-cpu", "host",
            "-smp", "2",
            "-m", "4096",
            "-drive", f"if=pflash,format=raw,readonly=on,file={ovmf_code}",
            "-drive", f"if=pflash,format=raw,file={ovmf_vars}",
            "-drive", f"file={overlay},format=qcow2,if=virtio",
            "-device", f"vfio-pci,host={gpu_addr}",
            "-netdev", f"user,id=net0,hostfwd=tcp:127.0.0.1:{ssh_port}-:22",
            "-device", "virtio-net-pci,netdev=net0",
            "-nographic",
        ]

        # ---- Launch VM ----

        print(f"  + Starting test VM")
        print(f"  + Log: {qemu_log}")

        with open(qemu_log, "w") as log_fh:
            log_fh.write(f"CMD: {' '.join(qemu_cmd)}\n\n")
            log_fh.flush()

            proc = subprocess.Popen(qemu_cmd, stdout=log_fh, stderr=log_fh)

            # Give QEMU a moment to start or fail
            time.sleep(3)
            if proc.poll() is not None:
                print(f"  ! QEMU exited immediately (code {proc.returncode})")
                log_fh.flush()
                _show_log_tail(qemu_log)
                return False

            print(f"  + QEMU running (pid {proc.pid}), waiting for SSH...")

            ssh_ready = False
            deadline = time.time() + 120
            attempts = 0
            while time.time() < deadline:
                time.sleep(3)
                attempts += 1
                if proc.poll() is not None:
                    print(f"  ! VM exited (code {proc.returncode})")
                    log_fh.flush()
                    _show_log_tail(qemu_log)
                    return False
                r = run(_ssh_cmd(ssh_port, ssh_key, "true"), check=False)
                if r.returncode == 0:
                    ssh_ready = True
                    break

            if not ssh_ready:
                print(f"  ! SSH not ready after {attempts} attempts")
                _show_log_tail(qemu_log)
                proc.terminate()
                try:
                    proc.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
                return False

            print("  + SSH ready, checking nvidia-smi")
            r = run(_ssh_cmd(ssh_port, ssh_key, "which nvidia-smi"), check=False)

            if r.returncode != 0:
                print("  + nvidia-smi not found, installing NVIDIA driver...")
                install_cmd = (
                    "export DEBIAN_FRONTEND=noninteractive; "
                    "apt-get update -qq && "
                    "apt-get install -y -qq nvidia-driver-560 || "
                    "apt-get install -y -qq nvidia-driver || "
                    "{ "
                    '  distribution=$(. /etc/os-release; echo ${ID}${VERSION_ID} | sed "s/\\.//g"); '
                    "  wget -q https://developer.download.nvidia.com/compute/cuda/repos/${distribution}/x86_64/cuda-keyring_1.1-1_all.deb; "
                    "  dpkg -i cuda-keyring_1.1-1_all.deb; "
                    "  apt-get update -qq && apt-get install -y -qq cuda-drivers; "
                    "}"
                )
                dr = run(_ssh_cmd(ssh_port, ssh_key, install_cmd), check=False)
                if dr.returncode != 0:
                    print("  ! NVIDIA driver installation failed")
                    if dr.stderr:
                        print(f"    {dr.stderr.strip()[-300:]}")
                else:
                    print("  + NVIDIA driver installed")

            # ---- Test nvidia-smi ----

            r = run(
                _ssh_cmd(ssh_port, ssh_key,
                         "nvidia-smi --query-gpu=name,memory.total,driver_version "
                         "--format=csv,noheader,nounits"),
                check=False,
            )

            success = False
            if r.returncode == 0 and r.stdout.strip():
                print(f"  + GPU in VM: {r.stdout.strip().splitlines()[0]}")
                success = True
                _save_gpu_info(ssh_port, ssh_key)
            else:
                print("  ! nvidia-smi failed inside VM")
                if r.stdout:
                    print(f"    stdout: {r.stdout.strip()[:300]}")
                if r.stderr:
                    print(f"    stderr: {r.stderr.strip()[:300]}")

            # ---- Shutdown ----

            print("  + Shutting down test VM")
            run(_ssh_cmd(ssh_port, ssh_key, "poweroff"), check=False)
            try:
                proc.wait(timeout=20)
            except subprocess.TimeoutExpired:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()

        # Commit overlay changes (installed drivers, etc.) back into the base image.
        if success:
            run([
                "qemu-img", "commit", str(overlay),
            ], check=False)

        return success

    except Exception as e:
        print(f"  ! Test error: {e}")
        return False

    finally:
        for f in cleanup_files:
            Path(f).unlink(missing_ok=True)


def restore_gpu_to_host(gpu_addr):
    """Unbind a GPU from vfio-pci and trigger kernel re-probe."""
    device_dir = Path(f"/sys/bus/pci/devices/{gpu_addr}")
    vfio_unbind = Path("/sys/bus/pci/drivers/vfio-pci/unbind")
    override = device_dir / "driver_override"
    probe = Path("/sys/bus/pci/drivers_probe")

    try:
        vfio_unbind.write_text(gpu_addr)
    except OSError:
        pass
    try:
        override.write_text("\n")
    except OSError:
        pass
    try:
        probe.write_text(gpu_addr)
    except OSError:
        pass


def bind_gpu_to_vfio(gpu_addr):
    """Bind a PCI device to vfio-pci driver."""
    device_dir = Path(f"/sys/bus/pci/devices/{gpu_addr}")

    if not device_dir.exists():
        print(f"  ! Device {gpu_addr} not found in sysfs")
        return False

    run(["modprobe", "vfio-pci"], check=False)

    driver_link = device_dir / "driver"
    if driver_link.exists():
        current = os.path.basename(os.readlink(str(driver_link)))
        if current == "vfio-pci":
            return True
        unbind_path = device_dir / "driver" / "unbind"
        try:
            unbind_path.write_text(gpu_addr)
        except OSError as e:
            print(f"  ! Cannot unbind {gpu_addr} from {current}: {e}")
            return False
        time.sleep(0.5)

    override = device_dir / "driver_override"
    try:
        override.write_text("vfio-pci")
    except OSError as e:
        print(f"  ! Cannot set driver_override for {gpu_addr}: {e}")
        return False

    probe = Path("/sys/bus/pci/drivers_probe")
    try:
        probe.write_text(gpu_addr)
    except OSError as e:
        print(f"  ! Cannot probe {gpu_addr}: {e}")
        return False

    time.sleep(0.5)

    if driver_link.exists():
        bound = os.path.basename(os.readlink(str(driver_link)))
        if bound != "vfio-pci":
            print(f"  ! {gpu_addr} bound to '{bound}', expected 'vfio-pci'")
            return False
    else:
        print(f"  ! {gpu_addr} has no driver after probe")
        return False

    return True


def find_iommu_group_devices(gpu_addr):
    """Find all PCI devices in the same IOMMU group as gpu_addr."""
    group_path = Path(f"/sys/bus/pci/devices/{gpu_addr}/iommu_group/devices")
    if not group_path.exists():
        return [gpu_addr]
    addrs = []
    for entry in sorted(group_path.iterdir()):
        name = entry.name
        device_dir = Path(f"/sys/bus/pci/devices/{name}")
        class_file = device_dir / "class"
        if class_file.exists():
            cls = class_file.read_text().strip()
            # Skip PCI bridges (0x0604xx)
            if cls.startswith("0x0604"):
                continue
        addrs.append(name)
    return addrs


def run_gpu_test(gpus):
    """Full GPU passthrough test: bind -> boot VM -> nvidia-smi -> teardown.

    Binds all devices in the IOMMU group (GPU + audio). Exits on failure.
    """
    gpu = gpus[0]
    gpu_addr = gpu["addr"]

    group_devices = find_iommu_group_devices(gpu_addr)
    print(f"\n-> Binding IOMMU group devices to VFIO for test")
    for addr in group_devices:
        print(f"  + Binding {addr}")
        if not bind_gpu_to_vfio(addr):
            # Restore already-bound devices
            for prev in group_devices:
                if prev == addr:
                    break
                restore_gpu_to_host(prev)
            sys.exit(f"Failed to bind {addr} to VFIO")

    ok = test_gpu_passthrough(gpu_addr)

    print("  + Restoring IOMMU group devices to host")
    for addr in reversed(group_devices):
        restore_gpu_to_host(addr)

    if ok:
        print("\n" + "=" * 50)
        print("  GPU PASSTHROUGH TEST PASSED")
        print("=" * 50)
        print(f"\n  {gpu['name']} ({gpu_addr}) works inside VM.")
        print("  Proceeding with agent setup.\n")
    else:
        print("\n" + "=" * 50)
        print("  GPU PASSTHROUGH TEST FAILED")
        print("=" * 50)
        print(f"\n  {gpu['name']} ({gpu_addr}) did not respond inside VM.")
        print(f"  Log: {RUN_DIR / 'gpu-test.log'}")
        print()
        print("  Common causes:")
        print("    - OVMF not installed: apt-get install ovmf")
        print("    - vfio-pci module not loaded: modprobe vfio-pci")
        print("    - All IOMMU group devices must be bound to vfio-pci")
        print("    - Base image missing NVIDIA drivers\n")
        sys.exit(1)


# ---------------------------------------------------------------------------
# Package installation
# ---------------------------------------------------------------------------

def install_base():
    print("\n-> Installing base packages")
    run(["apt-get", "update"])
    apt_install(
        ["curl", "wget", "ca-certificates", "gnupg", "lsb-release", "pciutils"]
    )


def install_qemu():
    print("\n-> Installing QEMU")
    packages = ["qemu-system-x86", "ovmf", "libguestfs-tools", "qemu-utils", "guestfs-tools"]
    apt_install(packages)

    # libguestfs needs a kernel; try to install the distro meta-package,
    # but don't fail — the running kernel is usually enough.
    kernel_pkg = get_kernel_package()
    if kernel_pkg:
        r = run(["apt-get", "install", "-y", "--allow-downgrades", kernel_pkg], check=False)
        if r.returncode != 0:
            print(f"  ! kernel meta-package '{kernel_pkg}' unavailable — using existing kernel")

    # Rebuild libguestfs appliance if the tool exists.
    if shutil.which("update-guestfs-appliance"):
        run(["update-guestfs-appliance"], check=False)
    print("  + Installed")


def install_frpc():
    print("\n-> Installing FRPC")
    if FRPC_BINARY.exists():
        print("  + Already installed")
        return

    run(
        [
            "bash",
            "-c",
            f'curl -fsSL "https://github.com/fatedier/frp/releases/download/v{FRP_VERSION}/'
            f'frp_{FRP_VERSION}_linux_amd64.tar.gz" | tar -xz',
        ]
    )
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
        print("  + Installing Go")
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
    run(
        ["go", "build", "-o", str(BINARY_PATH), "./cmd/agent"],
        cwd=str(INSTALL_DIR),
        env=env,
    )
    BINARY_PATH.chmod(0o755)
    print(f"  + {BINARY_PATH}")


# ---------------------------------------------------------------------------
# Systemd service
# ---------------------------------------------------------------------------

def create_service(api_key, gpus, debug, service_url, test_mode=False):
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

    exec_start = str(BINARY_PATH)
    if test_mode:
        exec_start += " --test"

    unit = textwrap.dedent(
        f"""\
        [Unit]
        Description=QuData Agent
        After=network.target

        [Service]
        Type=simple
        ExecStart={exec_start}
        Restart=always
        RestartSec=10
        {chr(10).join(env)}

        [Install]
        WantedBy=multi-user.target
    """
    )

    SYSTEMD_UNIT.write_text(unit)
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", AGENT_NAME])
    print(f"  + {SYSTEMD_UNIT}")


def start_service():
    print("\n-> Starting agent")
    run(["systemctl", "restart", AGENT_NAME])

    time.sleep(3)

    if (
        run(
            ["systemctl", "is-active", "--quiet", AGENT_NAME], check=False
        ).returncode
        != 0
    ):
        run(
            ["journalctl", "-u", AGENT_NAME, "-n", "30", "--no-pager"],
            capture=False,
        )
        sys.exit("Agent failed to start")

    print("  + Running")


# ---------------------------------------------------------------------------
# Phases
# ---------------------------------------------------------------------------

def phase1(args):
    print("\n" + "=" * 50)
    print("  QuData Agent Installer")
    print("=" * 50)

    if args.debug:
        print("\n  DEBUG MODE — no GPU passthrough\n")

    gpus = detect_gpus()
    audio = detect_gpu_audio_devices(gpus)

    if not gpus and not args.debug:
        sys.exit("No NVIDIA GPU detected. Use --debug for testing.")

    if gpus:
        print(f"\n-> Found {len(gpus)} GPU(s):")
        for gpu in gpus:
            print(f"  + {gpu['addr']}: {gpu['name']}")

    install_base()
    install_qemu()
    install_frpc()
    build_agent(args.binary)

    if args.debug:
        create_service(args.api_key, [], True, args.service_url, args.test)
        stop_running_agent()
        download_base_image()
        prepare_base_image()
        start_service()
        print_success()
        return

    iommu_ok = check_iommu_enabled()

    if not iommu_ok:
        configure_iommu(gpus, audio)

        save_state(
            {
                "phase": 2,
                "api_key": args.api_key,
                "gpus": gpus,
                "service_url": args.service_url,
                "binary": args.binary,
                "test_mode": args.test,
            }
        )

        continue_unit = textwrap.dedent(
            f"""\
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
        """
        )

        CONTINUE_UNIT.write_text(continue_unit)
        run(["systemctl", "daemon-reload"])
        run(["systemctl", "enable", "qudata-install-continue.service"])

        print("\n" + "=" * 50)
        print("  REBOOT REQUIRED")
        print("=" * 50)
        print("\n  IOMMU configured. Reboot to continue.\n")

        answer = input("  Reboot now? [y/N]: ").strip().lower()
        if answer in ("y", "yes"):
            run(["reboot"])
        else:
            print("  Run 'sudo reboot' when ready.\n")
    else:
        phase2_continue(args.api_key, gpus, args.service_url, args.test)


def phase2_continue(api_key, gpus, service_url, test_mode=False):
    print("\n" + "=" * 50)
    print("  QuData Agent Installer — Phase 2")
    print("=" * 50)

    run(["systemctl", "disable", "qudata-install-continue.service"], check=False)
    CONTINUE_UNIT.unlink(missing_ok=True)

    if not check_iommu_enabled():
        sys.exit("IOMMU still not enabled after reboot.")

    print("\n-> IOMMU is active")

    stop_running_agent()
    download_base_image()
    prepare_base_image()

    if gpus:
        run_gpu_test(gpus)

    create_service(api_key, gpus, False, service_url, test_mode)
    start_service()

    STATE_FILE.unlink(missing_ok=True)
    print_success()


def print_success():
    print("\n" + "=" * 50)
    print("  Installation complete!")
    print("=" * 50)
    print(f"\n  Status:  systemctl status {AGENT_NAME}")
    print(f"  Logs:    journalctl -u {AGENT_NAME} -f\n")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main():
    p = argparse.ArgumentParser(description="Install QuData Agent")
    p.add_argument("api_key", nargs="?", help="API key (ak-...)")
    p.add_argument("--binary", metavar="PATH", help="Pre-built binary")
    p.add_argument("--debug", action="store_true", help="Debug mode (no GPU)")
    p.add_argument("--test", action="store_true", help="Test mode (no FRPC, public ports)")
    p.add_argument("--service-url", metavar="URL", help="API URL override")
    p.add_argument(
        "--continue", dest="cont", action="store_true", help="Continue after reboot"
    )
    args = p.parse_args()

    if os.geteuid() != 0:
        sys.exit("Must run as root")

    if args.cont:
        state = load_state()
        if not state:
            sys.exit("No saved state found")
        phase2_continue(state["api_key"], state["gpus"], state.get("service_url"), state.get("test_mode", False))
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
