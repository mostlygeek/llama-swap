#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run as root: sudo $0" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYSTEMD_DIR="$SCRIPT_DIR/systemd"

install -D -m 0755 "$SCRIPT_DIR/nvmeof-initiator-canary.sh" /usr/local/sbin/nvmeof-initiator-canary.sh
install -D -m 0755 "$SCRIPT_DIR/net-tune-canary.sh" /usr/local/sbin/net-tune-canary.sh

install -D -m 0644 "$SYSTEMD_DIR/nvmeof-connect@.service" /etc/systemd/system/nvmeof-connect@.service
install -D -m 0644 "$SYSTEMD_DIR/net-tune-canary.service" /etc/systemd/system/net-tune-canary.service

install -D -m 0644 "$SYSTEMD_DIR/nvmeof-connect.env.sample" /etc/swap-laboratories/nvmeof/models.env
install -D -m 0644 "$SYSTEMD_DIR/net-tune.env.sample" /etc/swap-laboratories/net-tune.env

systemctl daemon-reload

cat <<'EOF'
Installed:
  /usr/local/sbin/nvmeof-initiator-canary.sh
  /usr/local/sbin/net-tune-canary.sh
  /etc/systemd/system/nvmeof-connect@.service
  /etc/systemd/system/net-tune-canary.service

Sample env files:
  /etc/swap-laboratories/nvmeof/models.env
  /etc/swap-laboratories/net-tune.env

Next steps:
  1) Edit /etc/swap-laboratories/nvmeof/models.env with real SUBSYSNQN/TRADDR.
  2) (Optional) Edit /etc/swap-laboratories/net-tune.env with IFACE and MODE.
  3) Enable services:
       systemctl enable --now net-tune-canary.service
       systemctl enable --now nvmeof-connect@models.service
  4) Verify:
       systemctl status net-tune-canary.service nvmeof-connect@models.service
       nvme list
EOF
