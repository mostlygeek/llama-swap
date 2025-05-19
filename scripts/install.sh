#!/bin/sh
# This script installs llama-swap on Linux.
# It detects the current operating system architecture and installs the appropriate version of llama-swap.

set -eu

red="$( (/usr/bin/tput bold || :; /usr/bin/tput setaf 1 || :) 2>&-)"
plain="$( (/usr/bin/tput sgr0 || :) 2>&-)"

status() { echo ">>> $*" >&2; }
error() { echo "${red}ERROR:${plain} $*"; exit 1; }
warning() { echo "${red}WARNING:${plain} $*"; }

available() { command -v $1 >/dev/null; }

SUDO=
if [ "$(id -u)" -ne 0 ]; then
    # Running as root, no need for sudo
    if ! available sudo; then
        error "This script requires superuser permissions. Please re-run as root."
    fi

    SUDO="sudo"
fi

[ "$(uname -s)" = "Linux" ] || error 'This script is intended to run on Linux only.'

ARCH=$(uname -m)
case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) error "Unsupported architecture: $ARCH" ;;
esac


download_binary() {
    ASSET_NAME="linux_$ARCH"

    # Fetch the latest release info and extract the matching asset URL
    DL_URL=$(curl -s "https://api.github.com/repos/mostlygeek/llama-swap/releases/latest" | \
        jq -r --arg name "$ASSET_NAME" \
        '.assets[] | select(.name | contains($name)) | .browser_download_url')

    # Check if a URL was successfully extracted
    if [ -z "$DL_URL" ]; then
        error "No matching asset found with name containing '$ASSET_NAME'."
    fi

    status "Downloading Linux $ARCH binary"
    curl -s -L "$DL_URL" | $SUDO tar -xzf - -C /usr/local/bin llama-swap
}
download_binary

configure_systemd() {
    if ! id llama-swap >/dev/null 2>&1; then
        status "Creating llama-swap user..."
        $SUDO useradd -r -s /bin/false -U -m -d /usr/share/llama-swap llama-swap
    fi
    if getent group render >/dev/null 2>&1; then
        status "Adding llama-swap user to render group..."
        $SUDO usermod -a -G render llama-swap
    fi
    if getent group video >/dev/null 2>&1; then
        status "Adding llama-swap user to video group..."
        $SUDO usermod -a -G video llama-swap
    fi
    if getent group docker >/dev/null 2>&1; then
        status "Adding llama-swap user to docker group..."
        $SUDO usermod -a -G docker llama-swap
    fi

    status "Adding current user to llama-swap group..."
    $SUDO usermod -a -G llama-swap $(whoami)

    if [ ! -f "/usr/share/llama-swap/config.yaml" ]; then
        status "Creating default config.yaml..."
        cat <<EOF | $SUDO -u llama-swap tee /usr/share/llama-swap/config.yaml >/dev/null
# default 15s likely to fail for default models due to downloading models
healthCheckTimeout: 60

models:
  "qwen2.5":
    cmd: |
      docker run
        --rm
        -p \${PORT}:8080
        --name qwen2.5
      ghcr.io/ggml-org/llama.cpp:server
        -hf bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
    cmdStop: docker stop qwen2.5

  "smollm2":
    cmd: |
      docker run
        --rm
        -p \${PORT}:8080
        --name smollm2
      ghcr.io/ggml-org/llama.cpp:server
        -hf bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M
    cmdStop: docker stop smollm2
EOF
    fi

    status "Creating llama-swap systemd service..."
    cat <<EOF | $SUDO tee /etc/systemd/system/llama-swap.service >/dev/null
[Unit]
Description=llama-swap
After=network.target

[Service]
User=llama-swap
Group=llama-swap

# set this to match your environment
ExecStart=/usr/local/bin/llama-swap --config /usr/share/llama-swap/config.yaml --watch-config

Restart=on-failure
RestartSec=3
StartLimitBurst=3
StartLimitInterval=30

[Install]
WantedBy=multi-user.target
EOF
    SYSTEMCTL_RUNNING="$(systemctl is-system-running || true)"
    case $SYSTEMCTL_RUNNING in
        running|degraded)
            status "Enabling and starting llama-swap service..."
            $SUDO systemctl daemon-reload
            $SUDO systemctl enable llama-swap

            start_service() { $SUDO systemctl restart llama-swap; }
            trap start_service EXIT
            ;;
        *)
            warning "systemd is not running"
            if [ "$IS_WSL2" = true ]; then
                warning "see https://learn.microsoft.com/en-us/windows/wsl/systemd#how-to-enable-systemd to enable it"
            fi
            ;;
    esac
}

if available systemctl; then
    configure_systemd
fi
