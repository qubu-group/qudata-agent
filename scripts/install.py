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
STATE_FILE = DATA_DIR / "install_state.json"

REPO_URL = os.environ.get(
    "REPO_URL", "https://github.com/qubu-group/qudata-agent.git"
)
GO_VERSION = "1.23.4"
FRP_VERSION = "0.61.1"

UBUNTU_CLOUD_IMAGE = (
    "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
)
BASE_IMAGE_PATH = IMAGE_DIR / "qudata-base.qcow2"


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
            match = re.search(r"\[([0-9a-f]{4}):([0-9a-f]{4})\]", line.lower())
            if match:
                gpus.append(
                    {
                        "addr": addr,
                        "vendor": match.group(1),
                        "device": match.group(2),
                        "name": line.split(":")[-1].strip().split("[")[0].strip(),
                    }
                )
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
          - sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
          - sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
          - systemctl enable ssh

          - apt-get clean
          - rm -rf /var/lib/apt/lists/*
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


def inject_ssh_key():
    priv_key = SSH_DIR / "management_key"
    pub_key = SSH_DIR / "management_key.pub"

    if not priv_key.exists():
        generate_ssh_key()

    pub_key_content = pub_key.read_text().strip()

    check = run(
        ["virt-cat", "-a", str(BASE_IMAGE_PATH), "/root/.ssh/authorized_keys"],
        check=False,
    )
    if pub_key_content not in check.stdout:
        print("  + Re-injecting SSH key into image")
        run(
            [
                "virt-customize",
                "-a",
                str(BASE_IMAGE_PATH),
                "--mkdir",
                "/root/.ssh",
                "--chmod",
                "0700:/root/.ssh",
                "--write",
                f"/root/.ssh/authorized_keys:{pub_key_content}",
                "--chmod",
                "0600:/root/.ssh/authorized_keys",
            ]
        )


# ---------------------------------------------------------------------------
# Test GPU passthrough
# ---------------------------------------------------------------------------

def test_gpu_passthrough(gpu_addr):
    """Boot a VM with GPU passthrough, verify nvidia-smi, then tear down.

    Returns True on success, False on failure. Always restores GPU to host.
    """
    print("\n-> Testing GPU passthrough")
    print(f"  + GPU: {gpu_addr}")

    ssh_key_path = SSH_DIR / "management_key"
    ssh_port = 2299

    overlay = IMAGE_DIR / "gpu-test.qcow2"
    run(
        [
            "qemu-img", "create", "-f", "qcow2",
            "-b", str(BASE_IMAGE_PATH), "-F", "qcow2",
            str(overlay),
        ]
    )

    qemu_cmd = [
        "qemu-system-x86_64",
        "-machine", "q35,accel=kvm",
        "-cpu", "host",
        "-smp", "2",
        "-m", "4096",
        "-drive", f"file={overlay},format=qcow2,if=virtio",
        "-device", f"vfio-pci,host={gpu_addr}",
        "-netdev", f"user,id=net0,hostfwd=tcp:127.0.0.1:{ssh_port}-:22",
        "-device", "virtio-net-pci,netdev=net0",
        "-nographic",
        "-bios", "/usr/share/OVMF/OVMF_CODE.fd",
    ]

    proc = None
    success = False

    try:
        print("  + Starting test VM")
        proc = subprocess.Popen(
            qemu_cmd,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

        print("  + Waiting for SSH (up to 120s)")
        ssh_ready = False
        deadline = time.time() + 120
        while time.time() < deadline:
            time.sleep(3)
            if proc.poll() is not None:
                print("  ! VM exited unexpectedly")
                break
            r = run(
                [
                    "ssh",
                    "-o", "StrictHostKeyChecking=no",
                    "-o", "UserKnownHostsFile=/dev/null",
                    "-o", "ConnectTimeout=5",
                    "-o", "BatchMode=yes",
                    "-o", "LogLevel=ERROR",
                    "-p", str(ssh_port),
                    "-i", str(ssh_key_path),
                    "root@127.0.0.1", "true",
                ],
                check=False,
            )
            if r.returncode == 0:
                ssh_ready = True
                break

        if not ssh_ready:
            print("  ! SSH not ready — test failed")
            return False

        print("  + SSH ready, checking nvidia-smi")
        r = run(
            [
                "ssh",
                "-o", "StrictHostKeyChecking=no",
                "-o", "UserKnownHostsFile=/dev/null",
                "-o", "BatchMode=yes",
                "-o", "LogLevel=ERROR",
                "-p", str(ssh_port),
                "-i", str(ssh_key_path),
                "root@127.0.0.1",
                "nvidia-smi --query-gpu=name,memory.total --format=csv,noheader",
            ],
            check=False,
        )

        if r.returncode == 0 and r.stdout.strip():
            gpu_info = r.stdout.strip().split("\n")[0]
            print(f"  + GPU detected in VM: {gpu_info}")
            success = True
        else:
            print("  ! nvidia-smi failed inside VM")
            if r.stderr:
                print(f"    {r.stderr.strip()[:200]}")
            success = False

    except Exception as e:
        print(f"  ! Test error: {e}")
        success = False

    finally:
        if proc and proc.poll() is None:
            print("  + Shutting down test VM")
            proc.terminate()
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()

        overlay.unlink(missing_ok=True)

    return success


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
    """Bind a GPU to vfio-pci for passthrough testing."""
    device_dir = Path(f"/sys/bus/pci/devices/{gpu_addr}")

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

    override = device_dir / "driver_override"
    try:
        override.write_text("vfio-pci")
    except OSError as e:
        print(f"  ! Cannot set driver_override: {e}")
        return False

    probe = Path("/sys/bus/pci/drivers_probe")
    try:
        probe.write_text(gpu_addr)
    except OSError as e:
        print(f"  ! Cannot probe driver: {e}")
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
        print(f"\n  GPU {gpu['name']} is working correctly inside VM.")
        print("  Proceeding with agent setup.\n")
    else:
        print("\n" + "=" * 50)
        print("  GPU PASSTHROUGH TEST FAILED")
        print("=" * 50)
        print(f"\n  GPU {gpu['name']} ({gpu_addr}) did not respond inside VM.")
        print("  Check IOMMU groups and VFIO configuration.")
        print("  Logs: /var/run/qudata/\n")
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
    apt_install(
        ["qemu-system-x86", "ovmf", "libguestfs-tools", "qemu-utils"]
    )
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

    unit = textwrap.dedent(
        f"""\
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
        create_service(args.api_key, [], True, args.service_url)
        download_base_image()
        inject_ssh_key()
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

        Path("/etc/systemd/system/qudata-install-continue.service").write_text(
            continue_unit
        )
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
        phase2_continue(args.api_key, gpus, args.service_url)


def phase2_continue(api_key, gpus, service_url):
    print("\n" + "=" * 50)
    print("  QuData Agent Installer — Phase 2")
    print("=" * 50)

    run(
        ["systemctl", "disable", "qudata-install-continue.service"], check=False
    )
    Path("/etc/systemd/system/qudata-install-continue.service").unlink(
        missing_ok=True
    )

    if not check_iommu_enabled():
        sys.exit("IOMMU still not enabled after reboot.")

    print("\n-> IOMMU is active")

    download_base_image()
    inject_ssh_key()

    if gpus:
        run_gpu_test(gpus)

    create_service(api_key, gpus, False, service_url)
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
