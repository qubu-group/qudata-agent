.PHONY: build build-docker extract install clean test lint

VERSION     ?= 0.1.0
BINARY      := qudata-agent
BUILD_DIR   := build
INSTALL_DIR := /usr/local/bin

CGO_LDFLAGS := -ldl

LDFLAGS := -X github.com/qudata/agent/internal/config.Version=$(VERSION) \
           -X github.com/qudata/agent/internal/config.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# ── Build locally (requires Linux + GCC) ──
build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 CGO_LDFLAGS="$(CGO_LDFLAGS)" \
		go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/agent

# ── Build inside Docker (works from any OS) ──
build-docker:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY)-builder .
	@echo "Build complete. Extract with: make extract"

# ── Extract binary from Docker image ──
extract: build-docker
	@mkdir -p $(BUILD_DIR)
	docker create --name $(BINARY)-tmp $(BINARY)-builder 2>/dev/null || true
	docker cp $(BINARY)-tmp:/usr/local/bin/$(BINARY) $(BUILD_DIR)/$(BINARY)
	docker rm $(BINARY)-tmp
	@echo "Binary extracted to $(BUILD_DIR)/$(BINARY)"

install: build
	install -m 0755 $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./... -v -count=1

lint:
	golangci-lint run ./...
