# ============================================================
# QuData Agent — Multi-stage Docker build
#
# Produces a statically-linked binary (except libc + libdl)
# that runs on any Linux x86_64 host with glibc.
#
# NVML is loaded dynamically at runtime via dlopen, so:
#   - NO libnvidia-ml needed at build time
#   - Binary works without GPU (falls back to debug/mock mode)
#   - On GPU hosts, the NVIDIA driver provides libnvidia-ml.so.1
#
# Usage:
#   docker build -t qudata-agent-builder .
#   docker run --rm qudata-agent-builder cat /qudata-agent > qudata-agent
#   chmod +x qudata-agent
#   scp qudata-agent target-host:/usr/local/bin/
# ============================================================

# ── Build stage ──
FROM golang:1.23-bookworm AS builder

# Install only GCC (for CGO + dlopen). No NVML headers needed!
RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc libc6-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

COPY . .

# Build with CGO for dlopen support
# -ldl links the dynamic loader (dlopen/dlsym)
ARG VERSION=dev
RUN CGO_ENABLED=1 \
    CGO_LDFLAGS="-ldl" \
    go build \
      -ldflags "-X github.com/qudata/agent/internal/config.Version=${VERSION} \
                -X github.com/qudata/agent/internal/config.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /qudata-agent \
      ./cmd/agent

# ── Output stage (minimal) ──
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /qudata-agent /usr/local/bin/qudata-agent

ENTRYPOINT ["/usr/local/bin/qudata-agent"]
