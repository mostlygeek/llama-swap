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

ui/node_modules:
	cd ui && npm install

# build the UI into internal/server/ui_dist; the `embed_ui` build tag embeds
# this output into the binary (see internal/server/embed.go)
ui: ui/node_modules
	cd ui && npm run build

# Build OSX binary
mac: ui
	@echo "Building Mac binary..."
	GOOS=darwin GOARCH=arm64 go build -tags embed_ui -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

# Build Linux binary
linux: linux-arm64 linux-amd64

linux-amd64: ui
	@echo "Building Linux AMD64 binary..."
	GOOS=linux GOARCH=amd64 go build -tags embed_ui -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

linux-arm64: ui
	@echo "Building Linux ARM64 binary..."
	GOOS=linux GOARCH=arm64 go build -tags embed_ui -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64

# Build Windows binary
windows: ui
	@echo "Building Windows binary..."
	GOOS=windows GOARCH=amd64 go build -tags embed_ui -ldflags="-X main.commit=${GIT_HASH} -X main.version=${GIT_VERSION} -X main.date=${BUILD_DATE}" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe

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

# Standalone browser Playground for a node started with -listen-tailcat. The
# page carries its own Tailcat client as a js/wasm module, so it reaches the
# node over the tunnel rather than over the network.
#
# Deliberately not a dependency of any binary target: nothing embeds this, and
# the wasm module takes ~40s and 27MB to build.
#
# The build tag list comes from the tailcat module itself so it stays right
# across upgrades. It is upstream's list for native binaries; their wasm list
# lives in an internal package that cannot be imported, and the difference is
# a couple of MB of code this page never calls.
#
# Also builds tailcat-playground-server for the host, a small binary with the
# split pair embedded, for when there is no web server to hand.
TAILCAT_DIST = $(BUILD_DIR)/tailcat-playground
TAILCAT_SERVER_DIST = cmd/tailcat-playground-server/dist
TAILCAT_TAGS = $(shell cat "$(shell go list -m -f '{{.Dir}}' github.com/tailscale/tailcat)/build-tags.txt")

tailcat-playground: ui/node_modules
	@echo "Building the Tailcat Playground page..."
	mkdir -p $(TAILCAT_DIST) ui/src/tailcat/generated
	cp "$(shell go env GOROOT)/lib/wasm/wasm_exec.js" ui/src/tailcat/generated/wasm_exec.js
	GOOS=js GOARCH=wasm go build -tags "$(TAILCAT_TAGS)" -ldflags="-s -w" \
		-o $(TAILCAT_DIST)/main.wasm ./cmd/tailcat-playground-wasm
	gzip -9 -f -k $(TAILCAT_DIST)/main.wasm
	cd ui && npm run build:tailcat
	node ui/scripts/build-tailcat.mjs $(TAILCAT_DIST)
	@echo "Building the Tailcat Playground server..."
	mkdir -p $(TAILCAT_SERVER_DIST)
	cp $(TAILCAT_DIST)/index.html $(TAILCAT_DIST)/main.wasm.gz $(TAILCAT_SERVER_DIST)/
	go build -tags embed_playground -ldflags="-s -w" -o $(TAILCAT_DIST)/tailcat-playground-server ./cmd/tailcat-playground-server

# Tests for the Tailcat Playground's js/wasm fetch bridge. They run under Node
# via the Go toolchain's own wasm runner, and stand an in-process HTTP server
# where the tunnel would be, so they need no DERP relay and no peer.
#
# Kept out of `test` and `test-all` because the js/wasm build links the whole
# Tailscale data plane and takes about a minute from cold.
#
# Compiling and running are separate steps on purpose. Go's wasm runner hands
# the whole environment to the wasm binary as argv, against a 4KB limit, and a
# containerised shell can exceed that on its own. Building keeps your
# environment (GOPROXY and friends); only the run gets a bare one.
TAILCAT_TEST_BIN = $(BUILD_DIR)/tailcat-bridge-test.wasm

test-wasm: $(BUILD_DIR)
	GOOS=js GOARCH=wasm go test -count=1 -tags "$(TAILCAT_TAGS)" \
		-c -o $(TAILCAT_TEST_BIN) ./cmd/tailcat-playground-wasm/
	env -i HOME="$$HOME" PATH="$(shell go env GOROOT)/lib/wasm:$$PATH" \
		go_js_wasm_exec ./$(TAILCAT_TEST_BIN) -test.v

test-ui:
	cd ui && npm ci && npm run check && npm test

# Score the Playground's Docs Agent against a local model. Builds and starts
# llama-swap itself; see evals/docs-agent/README.md for the tuning loop.
eval-docs-agent:
	./evals/docs-agent/run.sh $(EVAL_ARGS)

# Phony targets
.PHONY: all clean ui mac windows simple-responder simple-responder-windows test test-all test-dev test-ui wol-proxy eval-docs-agent
.PHONY: tailcat-playground test-wasm
.PHONY: linux linux-arm64 linux-amd64
