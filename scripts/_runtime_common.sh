#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
RUNTIME_DIR="${ROOT_DIR}/data/runtime"
LOG_DIR="${RUNTIME_DIR}/logs"
PID_DIR="${RUNTIME_DIR}/pids"
QDRANT_RUNTIME_DIR="${RUNTIME_DIR}/qdrant"
QDRANT_STORAGE_DIR="${ROOT_DIR}/data/index/qdrant"
QDRANT_SNAPSHOTS_DIR="${QDRANT_STORAGE_DIR}/snapshots"
QDRANT_TEMP_DIR="${QDRANT_STORAGE_DIR}/tmp"
QDRANT_PID_FILE="${PID_DIR}/qdrant.pid"
QDRANT_LOG_FILE="${LOG_DIR}/qdrant.log"
QDRANT_CONFIG_PATH="${QDRANT_RUNTIME_DIR}/config.yaml"
SEARXNG_ROOT_DIR="${ROOT_DIR}/tools/searxng"
SEARXNG_SOURCE_DIR="${SEARXNG_ROOT_DIR}/src"
SEARXNG_VENV_DIR="${SEARXNG_ROOT_DIR}/.venv"
SEARXNG_RUNTIME_DIR="${RUNTIME_DIR}/searxng"
SEARXNG_CONFIG_PATH="${SEARXNG_RUNTIME_DIR}/settings.yml"
SEARXNG_SECRET_PATH="${SEARXNG_RUNTIME_DIR}/secret"
SEARXNG_PID_FILE="${PID_DIR}/searxng.pid"
SEARXNG_LOG_FILE="${LOG_DIR}/searxng.log"
SEARXNG_HOST="${SEARXNG_HOST:-127.0.0.1}"
SEARXNG_PORT="${SEARXNG_PORT:-8888}"
QDRANT_HTTP_HOST="${QDRANT_HTTP_HOST:-127.0.0.1}"
QDRANT_HTTP_PORT="${QDRANT_HTTP_PORT:-6333}"
QDRANT_GRPC_PORT="${QDRANT_GRPC_PORT:-6334}"
LLAMA_SERVER_BIN="${ROOT_DIR}/llama.cpp/build/bin/llama-server"
LLAMA_SERVER_LIB_DIR="$(dirname "${LLAMA_SERVER_BIN}")"
MODEL_ROOT_DIR="${ROOT_DIR}/llama.cpp/models"

FAST_MODEL_DEFAULT="${ROOT_DIR}/llama.cpp/models/qwen3-8b-gguf/Qwen3-8B-Q6_K.gguf"
WORK_MODEL_DEFAULT="${ROOT_DIR}/llama.cpp/models/qwen3-30b-a3b-gguf/Qwen3-30B-A3B-Q4_K_M.gguf"
CODE_MODEL_DEFAULT="${ROOT_DIR}/llama.cpp/models/qwen3-coder-30b-a3b-gguf/Qwen3-Coder-30B-A3B-Instruct-UD-Q4_K_XL.gguf"
EMBEDDING_MODEL_DEFAULT="${ROOT_DIR}/llama.cpp/models/qwen3-embedding-0.6b-gguf/Qwen3-Embedding-0.6B-Q8_0.gguf"
RUNTIME_READY_TIMEOUT_SECONDS="${RUNTIME_READY_TIMEOUT_SECONDS:-180}"
RUNTIME_READY_INTERVAL_SECONDS="${RUNTIME_READY_INTERVAL_SECONDS:-1}"

mkdir -p "${LOG_DIR}" "${PID_DIR}" "${QDRANT_RUNTIME_DIR}" "${QDRANT_STORAGE_DIR}" "${QDRANT_SNAPSHOTS_DIR}" "${QDRANT_TEMP_DIR}" "${SEARXNG_RUNTIME_DIR}"

web_search_enabled() {
  local python_bin="${ROOT_DIR}/.venv/bin/python"
  if [[ ! -x "${python_bin}" ]]; then
    python_bin="$(command -v python3 || true)"
  fi
  if [[ -z "${python_bin}" ]]; then
    return 1
  fi
  PYTHONPATH="${ROOT_DIR}" "${python_bin}" -c 'from packages.config_core.loader import load_app_config; raise SystemExit(0 if load_app_config().web_search.enabled else 1)'
}

resolve_searxng_python() {
  resolve_existing_executable "${SEARXNG_VENV_DIR}/bin/python"
}

require_searxng_runtime() {
  local python_bin
  python_bin="$(resolve_searxng_python || true)"
  if [[ -z "${python_bin}" || ! -d "${SEARXNG_SOURCE_DIR}/searx" ]]; then
    cat >&2 <<EOF
local SearXNG runtime is not installed

Run:
  ./scripts/setup_searxng.sh
EOF
    exit 1
  fi
  printf '%s\n' "${python_bin}"
}

write_searxng_config() {
  if [[ ! -s "${SEARXNG_SECRET_PATH}" ]]; then
    if command -v openssl >/dev/null 2>&1; then
      openssl rand -hex 32 >"${SEARXNG_SECRET_PATH}"
    else
      printf '%s\n' "$(date +%s)-${RANDOM}-${RANDOM}" >"${SEARXNG_SECRET_PATH}"
    fi
    chmod 600 "${SEARXNG_SECRET_PATH}"
  fi
  local secret
  secret="$(cat "${SEARXNG_SECRET_PATH}")"
  if [[ -f "${SEARXNG_SOURCE_DIR}/searx/limiter.toml" ]]; then
    cp "${SEARXNG_SOURCE_DIR}/searx/limiter.toml" "${SEARXNG_RUNTIME_DIR}/limiter.toml"
  fi
  cat >"${SEARXNG_CONFIG_PATH}" <<EOF
use_default_settings:
  engines:
    keep_only:
      - duckduckgo
general:
  debug: false
  instance_name: Local LLM Workbench Search
search:
  safe_search: 1
  autocomplete: ''
  formats:
    - html
    - json
server:
  bind_address: ${SEARXNG_HOST}
  port: ${SEARXNG_PORT}
  secret_key: ${secret}
  limiter: false
  public_instance: false
  image_proxy: false
outgoing:
  request_timeout: 8.0
  max_request_timeout: 8.0
  enable_http2: true
EOF
}

start_searxng_foreground() {
  local python_bin
  python_bin="$(require_searxng_runtime)"
  write_searxng_config
  export SEARXNG_SETTINGS_PATH="${SEARXNG_CONFIG_PATH}"
  export SEARXNG_BIND_ADDRESS="${SEARXNG_HOST}"
  export SEARXNG_PORT
  cd "${SEARXNG_SOURCE_DIR}"
  exec "${python_bin}" -m searx.webapp
}

resolve_existing_path() {
  local candidate

  for candidate in "$@"; do
    if [[ -n "${candidate}" && -f "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

resolve_existing_executable() {
  local candidate

  for candidate in "$@"; do
    if [[ -n "${candidate}" && -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

find_model_by_pattern() {
  local pattern="$1"

  if [[ ! -d "${MODEL_ROOT_DIR}" ]]; then
    return 1
  fi

  find "${MODEL_ROOT_DIR}" -maxdepth 3 -type f -name "${pattern}" | sort | head -n 1
}

resolve_fast_model_path() {
  resolve_existing_path "${FAST_MODEL_PATH:-}" "${FAST_MODEL_DEFAULT}" || \
    find_model_by_pattern 'Qwen3-8B*.gguf'
}

resolve_work_model_path() {
  resolve_existing_path "${WORK_MODEL_PATH:-}" "${WORK_MODEL_DEFAULT}" || \
    find_model_by_pattern 'Qwen3-30B-A3B*.gguf'
}

resolve_code_model_path() {
  resolve_existing_path "${CODE_MODEL_PATH:-}" "${CODE_MODEL_DEFAULT}" || \
    find_model_by_pattern 'Qwen3-Coder-30B-A3B*.gguf'
}

resolve_embedding_model_path() {
  resolve_existing_path "${EMBEDDING_MODEL_PATH:-}" "${EMBEDDING_MODEL_DEFAULT}" || \
    find_model_by_pattern 'Qwen3-Embedding-0.6B*.gguf'
}

require_llama_server() {
  if [[ ! -x "${LLAMA_SERVER_BIN}" ]]; then
    echo "llama-server not found: ${LLAMA_SERVER_BIN}" >&2
    exit 1
  fi
}

configure_llama_server_library_path() {
  case "$(uname -s)" in
    Darwin)
      export DYLD_LIBRARY_PATH="${LLAMA_SERVER_LIB_DIR}${DYLD_LIBRARY_PATH:+:${DYLD_LIBRARY_PATH}}"
      ;;
    Linux)
      export LD_LIBRARY_PATH="${LLAMA_SERVER_LIB_DIR}${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"
      ;;
  esac
}

probe_llama_server() {
  (
    configure_llama_server_library_path
    "${LLAMA_SERVER_BIN}" --version >/dev/null 2>&1
  )
}

exec_llama_server() {
  configure_llama_server_library_path
  exec "${LLAMA_SERVER_BIN}" "$@"
}

exec_configured_model() {
  local role="$1"
  local fallback_path="$2"
  local fallback_alias="$3"
  local port="$4"
  if [[ -f "${ROOT_DIR}/configs/runtime-selection.local.json" ]]; then
    local python_bin="${EPHY_PYTHON_BIN:-${ROOT_DIR}/.venv/bin/python}"
    if [[ ! -x "${python_bin}" ]]; then
      echo 'model selection requires the runtime Python environment' >&2
      exit 1
    fi
    export PYTHONPATH="${ROOT_DIR}${PYTHONPATH:+:${PYTHONPATH}}"
    exec "${python_bin}" -m packages.model_registry --root "${ROOT_DIR}" launch \
      --role "${role}" --server "${LLAMA_SERVER_BIN}" \
      --fallback-path "${fallback_path}" --fallback-alias "${fallback_alias}"
  fi
  require_file "${fallback_path}" "${role} model"
  exec_llama_server -m "${fallback_path}" --host 127.0.0.1 --port "${port}" \
    --ctx-size 32768 --alias "${fallback_alias}" --n-gpu-layers 99
}

require_file() {
  local target="$1"
  local label="$2"

  if [[ ! -f "${target}" ]]; then
    echo "${label} not found: ${target}" >&2
    if [[ -d "${ROOT_DIR}/llama.cpp/models" ]]; then
      echo "available model files:" >&2
      find "${ROOT_DIR}/llama.cpp/models" -maxdepth 3 \( -name '*.gguf' -o -name '*.safetensors' \) | sort >&2
    fi
    exit 1
  fi
}

resolve_qdrant_bin() {
  local command_path=""

  if command -v qdrant >/dev/null 2>&1; then
    command_path="$(command -v qdrant)"
  fi

  resolve_existing_executable \
    "${QDRANT_BIN:-}" \
    "${ROOT_DIR}/bin/qdrant" \
    "${ROOT_DIR}/tools/qdrant/qdrant" \
    "${ROOT_DIR}/qdrant/qdrant" \
    "${command_path}" \
    "/opt/homebrew/bin/qdrant" \
    "/usr/local/bin/qdrant"
}

require_qdrant_bin() {
  local qdrant_bin
  qdrant_bin="$(resolve_qdrant_bin || true)"

  if [[ -z "${qdrant_bin}" ]]; then
    cat >&2 <<EOF
qdrant binary not found

Set QDRANT_BIN or place an executable at one of:
  ${ROOT_DIR}/bin/qdrant
  ${ROOT_DIR}/tools/qdrant/qdrant
  /opt/homebrew/bin/qdrant
  /usr/local/bin/qdrant
EOF
    exit 1
  fi

  printf '%s\n' "${qdrant_bin}"
}

write_qdrant_config() {
  cat >"${QDRANT_CONFIG_PATH}" <<EOF
log_level: INFO
storage:
  storage_path: ${QDRANT_STORAGE_DIR}
  snapshots_path: ${QDRANT_SNAPSHOTS_DIR}
  temp_path: ${QDRANT_TEMP_DIR}
service:
  host: ${QDRANT_HTTP_HOST}
  http_port: ${QDRANT_HTTP_PORT}
  grpc_port: ${QDRANT_GRPC_PORT}
cluster:
  enabled: false
telemetry_disabled: true
EOF
}

start_qdrant_foreground() {
  local qdrant_bin
  qdrant_bin="$(require_qdrant_bin)"
  write_qdrant_config
  exec "${qdrant_bin}" --config-path "${QDRANT_CONFIG_PATH}"
}

print_model_command() {
  local model_path="$1"
  local port="$2"
  local ctx_size="$3"
  local alias_name="$4"
  local extra_args="${5:-}"

  printf './llama.cpp/build/bin/llama-server -m %q --host 127.0.0.1 --port %q --ctx-size %q --alias %q' \
    "${model_path}" "${port}" "${ctx_size}" "${alias_name}"

  if [[ -n "${extra_args}" ]]; then
    printf ' %s' "${extra_args}"
  fi

  printf ' --n-gpu-layers 99\n'
}

is_pid_running() {
  local pid="$1"
  kill -0 "${pid}" 2>/dev/null
}

stop_pid_file() {
  local pid_file="$1"
  local name="$2"

  if [[ ! -f "${pid_file}" ]]; then
    return 0
  fi

  local pid
  pid="$(cat "${pid_file}")"

  if [[ -n "${pid}" ]] && is_pid_running "${pid}"; then
    kill "${pid}"
    echo "stopped ${name} (pid=${pid})"
  else
    echo "${name} was not running"
  fi

  rm -f "${pid_file}"
}

start_managed_process() {
  local name="$1"
  local script_path="$2"
  local log_path="$3"
  local pid_path="$4"

  if [[ -f "${pid_path}" ]]; then
    local existing_pid
    existing_pid="$(cat "${pid_path}")"
    if [[ -n "${existing_pid}" ]] && is_pid_running "${existing_pid}"; then
      echo "${name} is already running (pid=${existing_pid})"
      return 0
    fi
    rm -f "${pid_path}"
  fi

  nohup "${script_path}" >"${log_path}" 2>&1 &
  local pid=$!
  echo "${pid}" >"${pid_path}"
  echo "started ${name} (pid=${pid})"
}

wait_for_http_ready() {
  local name="$1"
  local url="$2"
  local pid_path="${3:-}"
  local log_path="${4:-}"
  local request_header="${5:-}"
  local started_at="${SECONDS}"

  echo "waiting for ${name}: ${url}"
  while true; do
    local curl_args=(--fail --silent --show-error --max-time 2)
    if [[ -n "${request_header}" ]]; then
      curl_args+=(-H "${request_header}")
    fi
    if curl "${curl_args[@]}" "${url}" >/dev/null 2>&1; then
      echo "${name} is ready"
      return 0
    fi

    if [[ -n "${pid_path}" && -f "${pid_path}" ]]; then
      local pid
      pid="$(cat "${pid_path}")"
      if [[ -z "${pid}" ]] || ! is_pid_running "${pid}"; then
        echo "${name} exited before becoming ready" >&2
        if [[ -n "${log_path}" && -f "${log_path}" ]]; then
          tail -n 80 "${log_path}" >&2
        fi
        return 1
      fi
    fi

    if (( SECONDS - started_at >= RUNTIME_READY_TIMEOUT_SECONDS )); then
      echo "timed out after ${RUNTIME_READY_TIMEOUT_SECONDS}s waiting for ${name}: ${url}" >&2
      if [[ -n "${log_path}" && -f "${log_path}" ]]; then
        tail -n 80 "${log_path}" >&2
      fi
      return 1
    fi
    sleep "${RUNTIME_READY_INTERVAL_SECONDS}"
  done
}
