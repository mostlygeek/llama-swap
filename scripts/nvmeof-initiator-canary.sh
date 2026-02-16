#!/usr/bin/env bash
set -euo pipefail

TRANSPORT="${TRANSPORT:-tcp}"
TRADDR="${TRADDR:-}"
TRSVCID="${TRSVCID:-4420}"
SUBSYSNQN="${SUBSYSNQN:-${NQN:-}}"
HOSTNQN="${HOSTNQN:-}"
KEEP_ALIVE_TMO="${KEEP_ALIVE_TMO:-300}"
CTRL_LOSS_TMO="${CTRL_LOSS_TMO:-600}"
RECONNECT_DELAY="${RECONNECT_DELAY:-10}"
QUEUE_SIZE="${QUEUE_SIZE:-128}"
DISCONNECT_FIRST=0
DISCONNECT_ONLY=0
DRY_RUN=0

usage() {
  cat <<'EOF'
Usage:
  nvmeof-initiator-canary.sh [options]

Options:
  --transport <tcp|rdma>   NVMe-oF transport (default: tcp)
  --traddr <ip-or-host>    Target IP/host
  --trsvcid <port>         Target service port (default: 4420)
  --subsysnqn <nqn>        NVMe subsystem NQN
  --hostnqn <nqn>          Optional host NQN
  --keep-alive-tmo <sec>   Keep-alive timeout (default: 300)
  --ctrl-loss-tmo <sec>    Controller loss timeout (default: 600)
  --reconnect-delay <sec>  Reconnect delay (default: 10)
  --queue-size <n>         Queue depth (default: 128)
  --disconnect-first       Disconnect subsystem before connect
  --disconnect-only        Only disconnect subsystem and exit
  --dry-run                Print commands without executing
  -h, --help               Show this help

Environment fallback:
  TRANSPORT, TRADDR, TRSVCID, SUBSYSNQN, HOSTNQN,
  KEEP_ALIVE_TMO, CTRL_LOSS_TMO, RECONNECT_DELAY, QUEUE_SIZE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --transport) TRANSPORT="$2"; shift 2 ;;
    --traddr) TRADDR="$2"; shift 2 ;;
    --trsvcid) TRSVCID="$2"; shift 2 ;;
    --subsysnqn|--nqn) SUBSYSNQN="$2"; shift 2 ;;
    --hostnqn) HOSTNQN="$2"; shift 2 ;;
    --keep-alive-tmo) KEEP_ALIVE_TMO="$2"; shift 2 ;;
    --ctrl-loss-tmo) CTRL_LOSS_TMO="$2"; shift 2 ;;
    --reconnect-delay) RECONNECT_DELAY="$2"; shift 2 ;;
    --queue-size) QUEUE_SIZE="$2"; shift 2 ;;
    --disconnect-first) DISCONNECT_FIRST=1; shift ;;
    --disconnect-only) DISCONNECT_ONLY=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

if ! command -v nvme >/dev/null 2>&1; then
  echo "nvme CLI not found. Install nvme-cli first." >&2
  exit 1
fi

if [[ -z "$SUBSYSNQN" ]]; then
  echo "SUBSYSNQN is required (--subsysnqn or env SUBSYSNQN)." >&2
  exit 1
fi

run() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '[dry-run] '
    printf '%q ' "$@"
    printf '\n'
    return 0
  fi
  "$@"
}

if [[ "$DISCONNECT_FIRST" -eq 1 || "$DISCONNECT_ONLY" -eq 1 ]]; then
  run nvme disconnect -n "$SUBSYSNQN" || true
fi

if [[ "$DISCONNECT_ONLY" -eq 1 ]]; then
  exit 0
fi

if [[ -z "$TRADDR" ]]; then
  echo "TRADDR is required for connect (--traddr or env TRADDR)." >&2
  exit 1
fi

connect_cmd=(
  nvme connect
  --transport "$TRANSPORT"
  --traddr "$TRADDR"
  --trsvcid "$TRSVCID"
  --nqn "$SUBSYSNQN"
  --keep-alive-tmo "$KEEP_ALIVE_TMO"
  --ctrl-loss-tmo "$CTRL_LOSS_TMO"
  --reconnect-delay "$RECONNECT_DELAY"
  --queue-size "$QUEUE_SIZE"
)

if [[ -n "$HOSTNQN" ]]; then
  connect_cmd+=(--hostnqn "$HOSTNQN")
fi

run "${connect_cmd[@]}"
echo "Connected NVMe-oF subsystem: $SUBSYSNQN ($TRANSPORT $TRADDR:$TRSVCID)"
