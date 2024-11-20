# Define variables for the application
APP_NAME = llama-swap
BUILD_DIR = build

# Default target: Builds binaries for both OSX and Linux
all: mac linux simple-responder

# Clean build directory
clean:
	rm -rf $(BUILD_DIR)

test:
	go test -short -v ./proxy

test-all:
	go test -v ./proxy

# Build OSX binary
mac:
	@echo "Building Mac binary..."
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

# Build Linux binary
linux:
	@echo "Building Linux binary..."
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

# for testing proxy.Process
simple-responder:
	@echo "Building simple responder"
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/simple-responder_darwin_arm64 misc/simple-responder/simple-responder.go
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/simple-responder_linux_amd64 misc/simple-responder/simple-responder.go

# Ensure build directory exists
$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# Phony targets
.PHONY: all clean osx linux
