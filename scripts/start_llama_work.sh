#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

MODEL_PATH="$(resolve_work_model_path || true)"
require_llama_server
exec_configured_model work "${MODEL_PATH}" qwen3-30b-a3b 8082
