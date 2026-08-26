#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_code_model_path || true)"
require_llama_server
exec_configured_model code "${MODEL_PATH}" qwen3-coder-30b-a3b 8083
