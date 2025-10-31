# Qudata Agent

## Installation

```bash
wget -qO- https://raw.githubusercontent.com/magicaleks/qudata-agent-alpha/main/install.sh | sudo bash -s YOUR_API_KEY
```

Or manual:

```bash
git clone https://github.com/magicaleks/qudata-agent-alpha.git
cd qudata-agent-alpha
sudo ./install.sh YOUR_API_KEY
```

## Requirements

- Ubuntu 22.04+
- NVIDIA GPU
- Root access

## Management

```bash
systemctl status qudata-agent
systemctl status qudata-security

journalctl -u qudata-agent -f
journalctl -u qudata-security -f

systemctl restart qudata-agent
systemctl restart qudata-security
```

## Build

```bash
CGO_ENABLED=1 go build -tags linux -o bin/qudata-agent ./cmd/app
CGO_ENABLED=1 go build -tags linux -o bin/qudata-security ./cmd/security
```

