#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_code_model_path || true)"
require_llama_server
exec_configured_model code "${MODEL_PATH}" qwen3.8-27b 8083 \
  --flash-attn on \
  --cache-type-k q8_0 \
  --cache-type-v q8_0 \
  --jinja \
  --reasoning-format deepseek \
  --reasoning-preserve
