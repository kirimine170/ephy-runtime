#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_embedding_model_path || true)"
require_llama_server
require_file "${MODEL_PATH}" "embedding model"

exec_llama_server \
  -m "${MODEL_PATH}" \
  --host 127.0.0.1 \
  --port 8090 \
  --ctx-size 4096 \
  --alias qwen3-embedding-0.6b \
  --embedding \
  --pooling last \
  --parallel 1 \
  --batch-size 512 \
  --ubatch-size 512 \
  --threads 8 \
  --n-gpu-layers 0
