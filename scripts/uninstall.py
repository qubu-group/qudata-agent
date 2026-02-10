#!/usr/bin/env python3
"""
QuData Agent — Uninstall Script

Stops the agent service, removes all installed files, and cleans up
the systemd unit. Docker and system packages are left intact unless
--purge is specified.

Usage:
    sudo python3 uninstall.py [--purge] [--keep-data]
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import List

AGENT_NAME = "qudata-agent"
BINARY_PATH = Path("/usr/local/bin") / AGENT_NAME
INSTALL_DIR = Path("/opt/qudata-agent")
DATA_DIR = Path("/var/lib/qudata")
LOG_DIR = Path("/var/log/qudata")
FRPC_DIR = Path("/etc/qudata")
FRPC_BINARY = Path("/usr/local/bin/frpc")
SYSTEMD_UNIT = Path(f"/etc/systemd/system/{AGENT_NAME}.service")
LOG_FILE = Path("/var/log/qudata-install.log")

def run(cmd, check=False):
    # type: (List[str], bool) -> subprocess.CompletedProcess
    """Run a shell command, suppressing output unless it fails."""
    result = subprocess.run(cmd, capture_output=True, text=True)
    if check and result.returncode != 0:
        print(f"  ⚠ Command failed: {' '.join(cmd)}")
        if result.stderr:
            print(f"    {result.stderr.strip()[:200]}")
    return result


def remove_path(path, label):
    # type: (Path, str) -> None
    """Remove a file or directory, logging the action."""
    if path.exists():
        if path.is_dir():
            shutil.rmtree(path, ignore_errors=True)
        else:
            path.unlink(missing_ok=True)
        print(f"  ✓ Removed {label}: {path}")
    else:
        print(f"  - {label} not found: {path} (skipped)")

def stop_service():
    """Stop and disable the systemd service."""
    print("\n→ Stopping agent service")

    result = run(["systemctl", "is-active", "--quiet", AGENT_NAME])
    if result.returncode == 0:
        run(["systemctl", "stop", AGENT_NAME])
        print(f"  ✓ Service stopped")
    else:
        print(f"  - Service not running")

    run(["systemctl", "disable", AGENT_NAME])
    print(f"  ✓ Service disabled")


def stop_frpc():
    """Kill any running frpc processes started by the agent."""
    print("\n→ Stopping FRPC processes")
    result = run(["pkill", "-f", "frpc.*qudata"], check=False)
    if result.returncode == 0:
        print("  ✓ FRPC processes terminated")
    else:
        print("  - No FRPC processes found")


def stop_containers():
    """Stop and remove any Docker containers created by the agent."""
    print("\n→ Cleaning Docker containers")

    result = run(["docker", "ps", "-aq"])
    if result.returncode == 0 and result.stdout.strip():
        containers = result.stdout.strip().split("\n")
        for cid in containers:
            cid = cid.strip()
            if cid:
                run(["docker", "rm", "-f", cid])
        print(f"  ✓ Removed {len(containers)} container(s)")
    else:
        print("  - No containers found")


def remove_agent_files(keep_data: bool):
    """Remove agent binary, source, configs, and optionally data."""
    print("\n→ Removing agent files")

    remove_path(BINARY_PATH, "Agent binary")
    remove_path(INSTALL_DIR, "Source directory")
    remove_path(SYSTEMD_UNIT, "Systemd unit")
    remove_path(LOG_DIR, "Log directory")
    remove_path(LOG_FILE, "Install log")
    remove_path(FRPC_DIR, "FRPC config directory")

    if keep_data:
        print(f"  - Keeping data directory: {DATA_DIR}")
    else:
        remove_path(DATA_DIR, "Data directory")


def remove_frpc():
    """Remove the FRPC binary."""
    print("\n→ Removing FRPC")
    remove_path(FRPC_BINARY, "FRPC binary")


def purge_docker():
    """Remove all Docker images (only with --purge)."""
    print("\n→ Purging Docker images")

    result = run(["docker", "images", "-q"])
    if result.returncode == 0 and result.stdout.strip():
        images = result.stdout.strip().split("\n")
        for img in images:
            img = img.strip()
            if img:
                run(["docker", "rmi", "-f", img])
        print(f"  ✓ Removed {len(images)} image(s)")
    else:
        print("  - No images found")


def reload_systemd():
    """Reload systemd daemon after removing the unit file."""
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "reset-failed"], check=False)
    print("  ✓ Systemd reloaded")

def main():
    parser = argparse.ArgumentParser(
        description="Uninstall the QuData Agent",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--purge", action="store_true",
                        help="Also remove Docker images and FRPC binary")
    parser.add_argument("--keep-data", action="store_true",
                        help="Keep persistent data in /var/lib/qudata")
    args = parser.parse_args()

    # Check root
    if os.geteuid() != 0:
        print("Error: This script must be run as root (use sudo)")
        sys.exit(1)

    print(f"\n{'='*60}")
    print(f"  QuData Agent Uninstaller")
    print(f"{'='*60}")

    if args.purge:
        print("\n  ⚠  PURGE mode: will also remove Docker images and FRPC")

    # Confirm
    try:
        response = input("\n  Proceed with uninstall? [y/N] ")
        if response.lower() not in ("y", "yes"):
            print("  Aborted.")
            sys.exit(0)
    except (EOFError, KeyboardInterrupt):
        print("\n  Aborted.")
        sys.exit(0)

    try:
        stop_service()
        stop_frpc()
        stop_containers()
        remove_agent_files(args.keep_data)

        if args.purge:
            remove_frpc()
            purge_docker()

        print("\n→ Finalizing")
        reload_systemd()

        print(f"\n{'='*60}")
        print(f"  ✓ Uninstall complete!")
        print(f"{'='*60}")

        if not args.purge:
            print(f"\n  Note: Docker and FRPC were kept. Use --purge to remove them.")
        if args.keep_data:
            print(f"  Note: Data directory preserved at {DATA_DIR}")
        print()

    except KeyboardInterrupt:
        print("\n  Uninstall interrupted.")
        sys.exit(130)
    except Exception as e:
        print(f"\n  ✗ Uninstall error: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
