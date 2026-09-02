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

# Default target: Builds binaries for both OSX and Linux
all: mac linux simple-responder

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

# The web UI under internal/server/ui_dist/ is hand-authored static HTML/CSS/JS,
# committed to the repo. There is no JS build step; the Go binary embeds it via
# //go:embed (see internal/server/ui.go), so build targets need no `ui` prereq
# or `embed_ui` tag.

# Build OSX binary
mac:
	@echo "Building Mac binary..."
	GOOS=darwin GOARCH=arm64 go build -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

# Build Linux binary
linux: linux-arm64 linux-amd64

linux-amd64:
	@echo "Building Linux AMD64 binary..."
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

linux-arm64:
	@echo "Building Linux ARM64 binary..."
	GOOS=linux GOARCH=arm64 go build -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64

# Build Windows binary
windows:
	@echo "Building Windows binary..."
	GOOS=windows GOARCH=amd64 go build -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe

# Security scan across the platforms the code compiles for. Must be zero findings.
gosec:
	GOOS=linux   gosec ./...
	GOOS=darwin  gosec ./...
	GOOS=windows gosec ./...

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
	echo "tagging new version: $$new_tag"; \
	git tag "$$new_tag";

GOOS ?= $(shell go env GOOS 2>/dev/null || echo linux)
GOARCH ?= $(shell go env GOARCH 2>/dev/null || echo amd64)
wol-proxy: $(BUILD_DIR)
	@echo "Building wol-proxy"
	go build -o $(BUILD_DIR)/wol-proxy-$(GOOS)-$(GOARCH)-$(shell date +%Y-%m-%d) cmd/wol-proxy/wol-proxy.go

# Score the Playground's Docs Agent against a local model. Builds and starts
# llama-swap itself; see evals/docs-agent/README.md for the tuning loop.
eval-docs-agent:
	./evals/docs-agent/run.sh $(EVAL_ARGS)

# Phony targets
.PHONY: all clean gosec mac windows simple-responder simple-responder-windows test test-all test-dev wol-proxy eval-docs-agent
.PHONY: linux linux-arm64 linux-amd64
