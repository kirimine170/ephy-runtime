#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_code_model_path || true)"
require_llama_server
require_file "${MODEL_PATH}" "code model"

exec_llama_server \
  -m "${MODEL_PATH}" \
  --host 127.0.0.1 \
  --port 8083 \
  --ctx-size 32768 \
  --alias qwen3-coder-30b-a3b \
  --n-gpu-layers 99
