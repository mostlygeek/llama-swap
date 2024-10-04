# Define variables for the application
APP_NAME = llamagate
BUILD_DIR = build

# Default target: Builds binaries for both OSX and Linux
all: mac linux

# Clean build directory
clean:
	rm -rf $(BUILD_DIR)

# Build OSX binary
mac:
	@echo "Building Mac binary..."
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64

# Build Linux binary
linux:
	@echo "Building Linux binary..."
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64

# for testing things
simple-responder:
	@echo "Building simple responder"
	go build -o $(BUILD_DIR)/simple-responder bin/simple-responder/simple-responder.go

# Ensure build directory exists
$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# Phony targets
.PHONY: all clean osx linux
