#!/usr/bin/env bash
set -euo pipefail

echo "[vllm-marlin-sm12x-build] Applying runtime patches..."

if ! command -v python3 >/dev/null 2>&1; then
  echo "[vllm-marlin-sm12x-build] python3 not found"
  exit 1
fi

if [[ -f patch_transformers.py ]]; then
  python3 patch_transformers.py
fi

if [[ -f patch_streaming.py ]]; then
  python3 patch_streaming.py
fi

MARLIN_UTILS="/usr/local/lib/python3.12/dist-packages/vllm/model_executor/layers/quantization/utils/marlin_utils.py"
if [[ -f "$MARLIN_UTILS" ]]; then
  if grep -q 'is_device_capability(120)' "$MARLIN_UTILS"; then
    sed -i 's/is_device_capability(120)/is_device_capability_family(120)/g' "$MARLIN_UTILS"
    find "$(dirname "$MARLIN_UTILS")" -type f -name '*.pyc' -delete || true
    echo "[vllm-marlin-sm12x-build] Patched marlin_utils capability check."
  else
    echo "[vllm-marlin-sm12x-build] marlin_utils already patched or pattern not found."
  fi
else
  echo "[vllm-marlin-sm12x-build] marlin_utils.py not found; skipping marlin_utils patch."
fi

MOE_DST="/usr/local/lib/python3.12/dist-packages/vllm/model_executor/layers/fused_moe/configs"
if [[ -d "moe-configs" && -d "$MOE_DST" ]]; then
  cp -f moe-configs/*.json "$MOE_DST"/
  echo "[vllm-marlin-sm12x-build] Copied tuned MoE configs to $MOE_DST."
else
  echo "[vllm-marlin-sm12x-build] MoE configs destination not found; skipping copy."
fi

echo "[vllm-marlin-sm12x-build] Done."
