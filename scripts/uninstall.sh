#!/bin/sh
# This script uninstalls llama-swap on Linux.
# It removes the binary, systemd service, config.yaml (optional), and llama-swap user and group.

set -eu

red="$( (/usr/bin/tput bold || :; /usr/bin/tput setaf 1 || :) 2>&-)"
plain="$( (/usr/bin/tput sgr0 || :) 2>&-)"

status() { echo ">>> $*" >&2; }
error() { echo "${red}ERROR:${plain} $*"; exit 1; }
warning() { echo "${red}WARNING:${plain} $*"; }

available() { command -v $1 >/dev/null; }

SUDO=
if [ "$(id -u)" -ne 0 ]; then
    if ! available sudo; then
        error "This script requires superuser permissions. Please re-run as root."
    fi

    SUDO="sudo"
fi

configure_systemd() {
    status "Stopping llama-swap service..."
    $SUDO systemctl stop llama-swap

    status "Disabling llama-swap service..."
    $SUDO systemctl disable llama-swap
}
if available systemctl; then
    configure_systemd
fi

if available llama-swap; then
    status "Removing llama-swap binary..."
    $SUDO rm $(which llama-swap)
fi

if [ -f "/usr/share/llama-swap/config.yaml" ]; then
    while true; do
        printf "Delete config.yaml (/usr/share/llama-swap/config.yaml)? [y/N] " >&2
        read answer
        case "$answer" in
            [Yy]* ) 
                $SUDO rm -r /usr/share/llama-swap
                break
                ;;
            [Nn]* | "" ) 
                break
                ;;
            * ) 
                echo "Invalid input. Please enter y or n."
                ;;
        esac
    done
fi

if id llama-swap >/dev/null 2>&1; then
    status "Removing llama-swap user..."
    $SUDO userdel llama-swap
fi

if getent group llama-swap >/dev/null 2>&1; then
    status "Removing llama-swap group..."
    $SUDO groupdel llama-swap
fi
