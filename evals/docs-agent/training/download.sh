#!/usr/bin/env bash
#
# Downloads the assets a training run needs. They are large, so they stay
# outside the repository, under $TRAINING_HOME (default ~/lfm3). Safe to re-run:
# files already present are skipped.
#
# Usage:
#   ./download.sh              stock safetensors, the training input
#   ./download.sh --gguf       also the stock GGUF quantizations
#   ./download.sh --cookbook   also Liquid's fine-tuning notebooks

set -euo pipefail

TRAINING_HOME="${TRAINING_HOME:-$HOME/lfm3}"
MODEL="LiquidAI/LFM3-1.7B"
GGUF=0
COOKBOOK=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --gguf)     GGUF=1; shift ;;
    --cookbook) COOKBOOK=1; shift ;;
    -h|--help)  sed -n '2,11p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0 ;;
    *)          echo "error: unknown option $1" >&2; exit 1 ;;
  esac
done

if ! command -v hf >/dev/null; then
  echo "error: the hf CLI is required: uv tool install huggingface_hub" >&2
  exit 1
fi

echo "==> $MODEL -> $TRAINING_HOME/base-hf"
hf download "$MODEL" --local-dir "$TRAINING_HOME/base-hf" --exclude "assets/*"

if [[ "$GGUF" == "1" ]]; then
  echo "==> $MODEL-GGUF -> $TRAINING_HOME/base-gguf"
  hf download "$MODEL-GGUF" \
    LFM3-1.7B-Q4_K_M.gguf LFM3-1.7B-QAD-Q4_0.gguf LICENSE README.md \
    --local-dir "$TRAINING_HOME/base-gguf"
fi

if [[ "$COOKBOOK" == "1" ]]; then
  echo "==> Liquid cookbook -> $TRAINING_HOME/cookbook"
  mkdir -p "$TRAINING_HOME/cookbook"
  base="https://raw.githubusercontent.com/Liquid4All/cookbook/main/finetuning/notebooks"
  for n in sft dpo; do
    curl -fsSL -o "$TRAINING_HOME/cookbook/lfm3_$n.ipynb" "$base/lfm3_${n}_with_trl.ipynb"
  done
fi
