# Define variables for the application
APP_NAME = llama-swap
BUILD_DIR = build

# Get closest tag or if that fails (no git repo or no tags) then devel
GIT_VERSION := $(shell git describe --abbrev=6 --tags 2>/dev/null || echo devel)
# Get the current Git hash
GIT_HASH := $(shell git rev-parse --short HEAD)
ifneq ($(shell git status --porcelain),)
    # There are untracked changes
    GIT_HASH := $(GIT_HASH)+
endif

# Capture the current build date in RFC3339 format
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Target platform for the `native`, `wol-proxy` and `kubeswap` targets.
# Override on the command line to cross compile: make GOOS=linux GOARCH=arm64
GOOS ?= $(shell go env GOOS 2>/dev/null || echo linux)
GOARCH ?= $(shell go env GOARCH 2>/dev/null || echo amd64)
ifeq ($(GOOS),windows)
EXE = .exe
else
EXE =
endif

LDFLAGS = -X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}

# Default target: build llama-swap for the platform running make
native: ui src
	@echo "Building $(APP_NAME) for $(GOOS)-$(GOARCH)..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-$(GOOS)-$(GOARCH)$(EXE)

# Build binaries for every platform this Makefile supports (see the per OS
# groups below) plus the test responder
all: mac linux windows freebsd simple-responder

# Clean build directory
clean:
	rm -rf $(BUILD_DIR)

# use cached test results while developing
test-dev:
	go test -short ./...
	staticcheck ./... || true

test:
	go test -short -count=1 ./internal/...

# for CI - full test (takes longer)
test-all:
	go test -race -count=1 ./internal/...

# Test suites for the cmd utilities (kubeswap, vllm-wrapper). Kept
# separate from test-all: those packages are unix-only (vllm-wrapper
# signals the serve proxy with syscall.Kill), so they must not be part
# of the target the Windows CI runs.
test-cmd:
	go test -race -count=1 ./cmd/kubeswap ./cmd/vllm-wrapper

# Lint and render the kubeswap Helm chart (requires helm on PATH).
# Renders every supported config shape (stock defaults, structured with
# each matrix builder, an existing ConfigMap) and asserts that the
# chart's own contradictory-input guards still fail rendering.
test-chart:
	helm lint cmd/kubeswap/chart
	@helm template llama-swap cmd/kubeswap/chart -n llama-swap > /dev/null \
		&& echo "chart: default (inline demo) render OK"
	@for f in cmd/kubeswap/chart/test-values/*.yaml; do \
		if ! helm template llama-swap cmd/kubeswap/chart -n llama-swap -f $$f > /dev/null; then \
			echo "chart: render FAILED: $$f"; exit 1; \
		fi; \
		echo "chart: render OK: $$f"; \
	done
	@for f in cmd/kubeswap/chart/test-values-invalid/*.yaml; do \
		if helm template llama-swap cmd/kubeswap/chart -n llama-swap -f $$f > /dev/null 2>&1; then \
			echo "chart: $$f was expected to fail rendering"; exit 1; \
		else \
			echo "chart: correctly rejected: $$f"; \
		fi; \
	done

ui/node_modules:
	cd ui && npm install

# build the UI into internal/server/ui_dist; the `embed_ui` build tag embeds
# this output into the binary (see internal/server/embed.go)
ui: ui/node_modules
	cd ui && npm run build

# copy the Go and TypeScript source, without tests, into
# internal/mcptools/sourcecode/_src; the `embed_src` build tag embeds it for
# the Help agent's code__* tools (see internal/mcptools/sourcecode/embed.go)
SRC_DIR = internal/mcptools/sourcecode/_src
src:
	rm -rf $(SRC_DIR)
	mkdir -p $(SRC_DIR)
	git ls-files -z --cached --others --exclude-standard -- '*.go' 'ui/*.ts' 'ui/*.svelte' \
		':(exclude)*_test.go' ':(exclude)*.test.ts' | tar -cf - --null -T - | tar -xf - -C $(SRC_DIR)

# One target per GOOS/GOARCH pair. CGO is off so these cross compile on any
# host, same as the release builds in .goreleaser.yaml. macOS is Apple Silicon
# only; Intel Macs are not supported.
darwin-arm64: ui src
	@echo "Building Darwin ARM64 binary..."
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

linux-amd64: ui src
	@echo "Building Linux AMD64 binary..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

linux-arm64: ui src
	@echo "Building Linux ARM64 binary..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64

windows-amd64: ui src
	@echo "Building Windows AMD64 binary..."
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe

freebsd-amd64: ui src
	@echo "Building FreeBSD AMD64 binary..."
	CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -tags embed_ui,embed_src -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-freebsd-amd64

# Per OS groups: the architectures built from this Makefile
mac: darwin-arm64
linux: linux-arm64 linux-amd64
windows: windows-amd64
freebsd: freebsd-amd64

# for testing with real external processes
simple-responder:
	@echo "Building simple responder"
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/simple-responder_darwin_arm64 cmd/simple-responder/simple-responder.go
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/simple-responder_linux_amd64 cmd/simple-responder/simple-responder.go

simple-responder-windows:
	@echo "Building simple responder for windows"
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/simple-responder.exe cmd/simple-responder/simple-responder.go

# Ensure build directory exists
$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# Create a new release tag
release:
	@echo "Checking for unstaged changes..."
	@if [ -n "$(shell git status --porcelain)" ]; then \
		echo "Error: There are unstaged changes. Please commit or stash your changes before creating a release tag." >&2; \
		exit 1; \
	fi

# Get the highest tag in v{number} format, increment it, and create a new tag
	@highest_tag=$$(git tag --sort=-v:refname | grep -E '^v[0-9]+$$' | head -n 1 || echo "v0"); \
	new_tag="v$$(( $${highest_tag#v} + 1 ))"; \
	echo "Checking for a changelog entry for: $$new_tag"; \
	if ! grep -qE "^## $$new_tag( |$$)" CHANGELOG.md; then \
		echo "Error: CHANGELOG.md has no entry for $$new_tag. Add a '## $$new_tag' section and commit it." >&2; \
		exit 1; \
	fi; \
	echo "tagging new version: $$new_tag"; \
	git tag "$$new_tag"

wol-proxy: $(BUILD_DIR)
	@echo "Building wol-proxy"
	go build -o $(BUILD_DIR)/wol-proxy-$(GOOS)-$(GOARCH)-$(shell date +%Y-%m-%d) cmd/wol-proxy/wol-proxy.go

# Build the kubeswap Kubernetes backend wrapper (host-side binary; also
# built into the unified image, see docker/unified/install-kubeswap.sh)
KUBESWAP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

kubeswap: $(BUILD_DIR)
	@echo "Building kubeswap"
	go build -trimpath -ldflags "-X main.version=$(KUBESWAP_VERSION) -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" -o $(BUILD_DIR)/kubeswap-$(GOOS)-$(GOARCH) ./cmd/kubeswap

test-ui:
	cd ui && npm ci && npm run check && npm test

# Score the Playground's Docs Agent against a local model. Builds and starts
# llama-swap itself; see evals/docs-agent/README.md for the tuning loop.
eval-docs-agent:
	./evals/docs-agent/run.sh $(EVAL_ARGS)

# Phony targets
.PHONY: all native clean ui src simple-responder simple-responder-windows test test-all test-chart test-dev test-ui wol-proxy kubeswap eval-docs-agent release
.PHONY: mac linux windows freebsd
.PHONY: darwin-arm64 linux-arm64 linux-amd64 windows-amd64 freebsd-amd64
