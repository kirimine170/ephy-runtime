#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_code_model_path || true)"
require_llama_server
require_file "${MODEL_PATH}" "code model"

exec "${LLAMA_SERVER_BIN}" \
  -m "${MODEL_PATH}" \
  --host 127.0.0.1 \
  --port 8083 \
  --ctx-size 32768 \
  --alias qwen3.8-27b \
  --n-gpu-layers 99 \
  --flash-attn on \
  --cache-type-k q8_0 \
  --cache-type-v q8_0 \
  --jinja \
  --reasoning-format deepseek \
  --reasoning-preserve
