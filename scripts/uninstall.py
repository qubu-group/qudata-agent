#!/usr/bin/env python3
"""QuData Agent — Uninstall Script.

Stops the agent, kills VMs, unbinds GPUs from VFIO, removes all files.
Works independently of the directory it is run from.

Usage:
    sudo python3 uninstall.py [--purge] [--keep-data] [-y]
"""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths (must stay in sync with install.py)
# ---------------------------------------------------------------------------

AGENT_NAME = "qudata-agent"
BINARY_PATH = Path("/usr/local/bin") / AGENT_NAME
INSTALL_DIR = Path("/opt/qudata-agent")
DATA_DIR = Path("/var/lib/qudata")
LOG_DIR = Path("/var/log/qudata")
RUN_DIR = Path("/var/run/qudata")
FRPC_DIR = Path("/etc/qudata")
FRPC_BINARY = Path("/usr/local/bin/frpc")
SYSTEMD_UNIT = Path(f"/etc/systemd/system/{AGENT_NAME}.service")
CONTINUE_UNIT = Path("/etc/systemd/system/qudata-install-continue.service")


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True, check=False)


def remove_path(path, label):
    if path.exists():
        if path.is_dir():
            shutil.rmtree(path, ignore_errors=True)
        else:
            path.unlink(missing_ok=True)
        print(f"  + Removed {label}: {path}")
    else:
        print(f"  - {label} not found (skipped)")


# ---------------------------------------------------------------------------
# Steps
# ---------------------------------------------------------------------------

def stop_service():
    print("\n-> Stopping agent service")
    r = run(["systemctl", "is-active", "--quiet", AGENT_NAME])
    if r.returncode == 0:
        run(["systemctl", "stop", AGENT_NAME])
        print("  + Service stopped")
    else:
        print("  - Service not running")
    run(["systemctl", "disable", AGENT_NAME])


def kill_vms():
    print("\n-> Killing QEMU VMs")
    r = run(["pgrep", "-f", "qemu-system"])
    if r.returncode == 0 and r.stdout.strip():
        run(["pkill", "-9", "-f", "qemu-system"])
        print("  + Killed QEMU processes")
    else:
        print("  - No QEMU processes found")


def unbind_vfio_gpus():
    print("\n-> Restoring GPUs from VFIO")
    vfio_dir = Path("/sys/bus/pci/drivers/vfio-pci")
    if not vfio_dir.exists():
        print("  - No VFIO driver loaded")
        return

    count = 0
    for entry in vfio_dir.iterdir():
        if not entry.is_symlink() or not entry.name.startswith("0000:"):
            continue

        addr = entry.name
        device_dir = Path(f"/sys/bus/pci/devices/{addr}")

        try:
            (vfio_dir / "unbind").write_text(addr)
        except OSError:
            continue

        try:
            (device_dir / "driver_override").write_text("\n")
        except OSError:
            pass

        try:
            Path("/sys/bus/pci/drivers_probe").write_text(addr)
        except OSError:
            pass

        count += 1

    if count:
        print(f"  + Unbound {count} device(s) from VFIO")
    else:
        print("  - No devices bound to VFIO")


def stop_frpc():
    print("\n-> Stopping FRPC")
    r = run(["pkill", "-f", "frpc"])
    if r.returncode == 0:
        print("  + FRPC terminated")
    else:
        print("  - No FRPC processes found")


def clean_runtime():
    """Remove QMP sockets, logs, and other runtime files."""
    if RUN_DIR.exists():
        for pattern in ("*.qmp", "*.log", "*.fd"):
            for f in RUN_DIR.glob(pattern):
                f.unlink(missing_ok=True)


def remove_agent_files(keep_data):
    print("\n-> Removing agent files")
    remove_path(BINARY_PATH, "Agent binary")
    remove_path(INSTALL_DIR, "Source directory")
    remove_path(SYSTEMD_UNIT, "Systemd unit")
    remove_path(CONTINUE_UNIT, "Continue unit")
    remove_path(LOG_DIR, "Log directory")
    remove_path(FRPC_DIR, "FRPC config directory")
    remove_path(RUN_DIR, "Runtime directory")

    if keep_data:
        print(f"  - Keeping data: {DATA_DIR}")
    else:
        remove_path(DATA_DIR, "Data directory")


def remove_frpc():
    print("\n-> Removing FRPC")
    remove_path(FRPC_BINARY, "FRPC binary")


def reload_systemd():
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "reset-failed"])
    print("  + Systemd reloaded")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Uninstall QuData Agent")
    parser.add_argument("--purge", action="store_true", help="Also remove FRPC binary")
    parser.add_argument("--keep-data", action="store_true", help="Keep /var/lib/qudata")
    parser.add_argument("-y", "--yes", action="store_true", help="Skip confirmation")
    args = parser.parse_args()

    if os.geteuid() != 0:
        sys.exit("Must run as root")

    print(f"\n{'=' * 50}")
    print("  QuData Agent Uninstaller")
    print(f"{'=' * 50}")

    if args.purge:
        print("\n  PURGE mode: will also remove FRPC binary")

    if not args.yes:
        try:
            answer = input("\n  Proceed? [y/N] ").strip().lower()
            if answer not in ("y", "yes"):
                print("  Aborted.")
                sys.exit(0)
        except (EOFError, KeyboardInterrupt):
            print("\n  Aborted.")
            sys.exit(0)

    try:
        stop_service()
        kill_vms()
        unbind_vfio_gpus()
        stop_frpc()
        clean_runtime()
        remove_agent_files(args.keep_data)

        if args.purge:
            remove_frpc()

        print("\n-> Finalizing")
        reload_systemd()

        print(f"\n{'=' * 50}")
        print("  Uninstall complete!")
        print(f"{'=' * 50}")

        if not args.purge:
            print(f"\n  FRPC binary kept. Use --purge to remove.")
        if args.keep_data:
            print(f"  Data preserved at {DATA_DIR}")
        print()

    except KeyboardInterrupt:
        print("\n  Interrupted.")
        sys.exit(130)
    except Exception as e:
        print(f"\n  Error: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
