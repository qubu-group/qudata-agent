#!/bin/bash
set -euo pipefail

BASE_URL="${REPO_URL:-https://raw.githubusercontent.com/qubu-group/qudata-agent/main}"

die() { echo "Error: $1" >&2; exit 1; }
info() { echo "-> $1"; }

[[ $EUID -eq 0 ]] || die "Run as root: curl -fsSL $BASE_URL/install.sh | sudo bash -s -- <api-key>"
command -v curl >/dev/null || die "curl is required"
command -v python3 >/dev/null || { apt-get update -qq && apt-get install -y -qq python3; }

info "Downloading installer..."
curl -fsSL "${BASE_URL}/scripts/install.py" -o /tmp/qudata-install.py

info "Running installer..."
python3 /tmp/qudata-install.py "$@"
rm -f /tmp/qudata-install.py
