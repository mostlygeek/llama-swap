#!/usr/bin/env bash
#
# Runs the Docs Agent eval against a llama-swap built from the working tree.
#
# The server has to be built from this branch: /api/mcp does not exist in
# releases, and docs/kb is compiled in by //go:embed, so a KB edit is only
# visible after a rebuild. Everything the agent model needs comes through the
# same server, via peers, so there is only ever one base URL.
#
# Usage:
#   ./run.sh                              build, start, eval, tear down
#   ./run.sh --repeat 3 --label prompt-v2  what to run before keeping a change
#   ./run.sh --concurrency 4              run up to four cases at once
#   ./run.sh --base-url http://localhost:8080   attach to a server already running
#   ./run.sh --holdout                    the held-out set, to check for overfitting

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

CONFIG="${DOCS_AGENT_CONFIG:-evals/config.yaml}"
MODEL="${DOCS_AGENT_MODEL:-gemma-4-12B}"
CONCURRENCY=1
PORT=18080
BASE_URL=""
CASES="evals/docs-agent/cases"
EMBED_UI=0
AGENT_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)   CONFIG="$2"; shift 2 ;;
    --model)    MODEL="$2"; shift 2 ;;
    --concurrency) CONCURRENCY="$2"; shift 2 ;;
    --port)     PORT="$2"; shift 2 ;;
    --base-url) BASE_URL="$2"; shift 2 ;;
    --cases)    CASES="$2"; shift 2 ;;
    --holdout)  CASES="evals/docs-agent/cases-holdout"; shift ;;
    --embed-ui) EMBED_UI=1; shift ;;
    -h|--help)  sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0 ;;
    # Everything else is forwarded: --repeat, --label, --only, --system-prompt,
    # --temperature, --out, --report ...
    *)          AGENT_ARGS+=("$1"); shift ;;
  esac
done

if [[ -z "$BASE_URL" ]]; then
  if [[ ! -f "$CONFIG" ]]; then
    echo "error: no llama-swap config at $CONFIG" >&2
    echo "  Pass --config, or set DOCS_AGENT_CONFIG." >&2
    echo "  It needs the agent model reachable -- locally, or through a peers: block." >&2
    echo "  See config.example.yaml for the format." >&2
    exit 1
  fi

  echo "==> building llama-swap"
  if [[ "$EMBED_UI" == "1" ]]; then
    make ui >/dev/null
    go build -tags embed_ui -o build/llama-swap .
  else
    # No embed_ui: the eval never loads the UI, and skipping it keeps a
    # rebuild-per-iteration down to a few seconds.
    go build -o build/llama-swap .
  fi

  echo "==> starting llama-swap on 127.0.0.1:$PORT"
  ./build/llama-swap -config "$CONFIG" -listen "127.0.0.1:$PORT" >"${TMPDIR:-/tmp}/docs-agent-swap.log" 2>&1 &
  SERVER_PID=$!
  trap 'kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true' EXIT

  BASE_URL="http://127.0.0.1:$PORT"

  # /health is registered without auth middleware, so it is the right probe
  # even when the config sets apiKeys.
  for _ in $(seq 1 60); do
    if curl -fsS -m 2 "$BASE_URL/health" >/dev/null 2>&1; then break; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "error: llama-swap exited during startup:" >&2
      tail -20 "${TMPDIR:-/tmp}/docs-agent-swap.log" >&2
      exit 1
    fi
    sleep 0.5
  done

  if ! curl -fsS -m 2 "$BASE_URL/health" >/dev/null 2>&1; then
    echo "error: llama-swap did not become healthy within 30s" >&2
    exit 1
  fi
fi

echo "==> $BASE_URL is up; running eval with $MODEL"
cd ui
exec npm run --silent agent -- eval \
  --base-url "$BASE_URL" \
  --model "$MODEL" \
  --concurrency "$CONCURRENCY" \
  --cases "../$CASES" \
  "${AGENT_ARGS[@]}"
