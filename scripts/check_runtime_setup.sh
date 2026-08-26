#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

FAST_MODEL_PATH="$(resolve_fast_model_path || true)"
WORK_MODEL_PATH="$(resolve_work_model_path || true)"
CODE_MODEL_PATH="$(resolve_code_model_path || true)"
EMBEDDING_MODEL_PATH="$(resolve_embedding_model_path || true)"
QDRANT_BIN_PATH="$(resolve_qdrant_bin || true)"
SEARXNG_PYTHON_PATH="$(resolve_searxng_python || true)"

status_line() {
  local label="$1"
  local target="$2"

  if [[ -e "${target}" ]]; then
    printf '[ok]   %s: %s\n' "${label}" "${target}"
  else
    printf '[miss] %s: %s\n' "${label}" "${target}"
  fi
}

echo "workspace: ${ROOT_DIR}"
status_line "llama-server" "${LLAMA_SERVER_BIN}"
CHECK_STATUS=0
if [[ ! -f "${LLAMA_SERVER_BIN}" ]]; then
  echo "[miss] llama-server runtime: executable is missing" >&2
  CHECK_STATUS=1
elif [[ ! -x "${LLAMA_SERVER_BIN}" ]]; then
  echo "[fail] llama-server runtime: file is not executable" >&2
  CHECK_STATUS=1
elif probe_llama_server; then
  echo "[ok]   llama-server runtime: dynamic libraries are loadable"
else
  echo "[fail] llama-server runtime: executable exists but cannot load its dynamic libraries" >&2
  CHECK_STATUS=1
fi
status_line "fast model" "${FAST_MODEL_PATH}"
status_line "work model" "${WORK_MODEL_PATH}"
status_line "code model" "${CODE_MODEL_PATH}"
status_line "embedding model" "${EMBEDDING_MODEL_PATH}"
status_line "models config" "${ROOT_DIR}/configs/models.yaml"
status_line "models local override" "${ROOT_DIR}/configs/models.local.yaml"
status_line "rag config" "${ROOT_DIR}/configs/rag.yaml"
status_line "rag local override" "${ROOT_DIR}/configs/rag.local.yaml"
status_line "web search config" "${ROOT_DIR}/configs/web.yaml"
status_line "web search local override" "${ROOT_DIR}/configs/web.local.yaml"
status_line "qdrant binary" "${QDRANT_BIN_PATH:-"(not found)"}"
if web_search_enabled; then
  status_line "searxng python" "${SEARXNG_PYTHON_PATH:-"(not installed)"}"
else
  echo "[off]  web search: configs/web.local.yaml is not enabled"
fi

cat <<EOF

recommended commands:
  phase1 entrypoint:
    ./scripts/phase1.sh
    ./scripts/phase1.sh full
    ./scripts/phase1.sh check
    ./scripts/phase1.sh commands
    ./scripts/phase1.sh restart
    ./scripts/phase1.sh stop
    ./scripts/phase1.sh qdrant
    ./scripts/phase1.sh qdrant-stop
    ./scripts/phase1.sh qdrant-restart
    ./scripts/phase1.sh searxng-setup
    ./scripts/phase1.sh searxng
    ./scripts/phase1.sh searxng-stop
    ./scripts/phase1.sh searxng-restart

  phase1 helper wrappers:
    ./scripts/start_phase1_stack.sh
    ./scripts/start_phase1_backend.sh
    ./scripts/start_phase1_ui.sh
    ./scripts/stop_phase1.sh

  consolidated entrypoint:
    ./scripts/workbench.sh check
    ./scripts/workbench.sh commands
    ./scripts/workbench.sh start-phase1
    ./scripts/workbench.sh start-full
    ./scripts/workbench.sh restart-full
    ./scripts/workbench.sh start-qdrant
    ./scripts/workbench.sh stop-qdrant
    ./scripts/workbench.sh restart-qdrant
    ./scripts/workbench.sh setup-searxng
    ./scripts/workbench.sh start-searxng
    ./scripts/workbench.sh stop-searxng
    ./scripts/workbench.sh restart-searxng

  docker-independent default stack:
    ./scripts/start_phase1_stack.sh
    ./scripts/start_backend_stack.sh
    ./scripts/start_wails.sh

  skip qdrant explicitly:
    ./scripts/start_backend_stack.sh --without-qdrant

  skip embedding explicitly:
    ./scripts/start_backend_stack.sh --without-embedding

  full feature stack:
    ./scripts/start_full_feature.sh
    ./scripts/start_complete_stack.sh

  full feature backends only:
    ./scripts/start_backend_stack.sh --with-embedding --with-qdrant

  compatibility aliases:
    ./scripts/full_feature.sh
    ./scripts/run_full_feature.sh
    ./scripts/start_ephy_runtime.sh

  command summary:
    ./scripts/print_startup_commands.sh
EOF
exit "${CHECK_STATUS}"
