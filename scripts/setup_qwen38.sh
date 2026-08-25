#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
MODEL_REPO="${QWEN38_MODEL_REPO:-unsloth/Qwen3.8-27B-GGUF}"
DEFAULT_MODEL_FILENAME="Qwen3.8-27B-Q4_K_M.gguf"
MODEL_FILENAME="${QWEN38_MODEL_FILENAME:-${DEFAULT_MODEL_FILENAME}}"
if [[ "${MODEL_FILENAME}" == "${DEFAULT_MODEL_FILENAME}" ]]; then
  EXPECTED_SHA256="${QWEN38_EXPECTED_SHA256:-7e78da5d7e3ae28d178121f58646953305f3e5bd3cb46f4a75584e8b6c6fe169}"
else
  EXPECTED_SHA256="${QWEN38_EXPECTED_SHA256:-}"
fi
MODEL_DIR="${QWEN38_MODEL_DIR:-${ROOT_DIR}/llama.cpp/models/qwen3.8-27b-gguf}"
MODEL_PATH="${MODEL_DIR}/${MODEL_FILENAME}"
PARTIAL_PATH="${MODEL_PATH}.part"
MODEL_URL="https://huggingface.co/${MODEL_REPO}/resolve/main/${MODEL_FILENAME}?download=true"

if [[ ! -x "${ROOT_DIR}/llama.cpp/build/bin/llama-server" ]]; then
  echo "llama-server not found: ${ROOT_DIR}/llama.cpp/build/bin/llama-server" >&2
  exit 1
fi

mkdir -p "${MODEL_DIR}"

if [[ -s "${MODEL_PATH}" ]]; then
  echo "Qwen3.8 already exists: ${MODEL_PATH}"
else
  echo "Downloading ${MODEL_REPO}/${MODEL_FILENAME}"
  curl \
    --fail \
    --location \
    --retry 3 \
    --show-error \
    --continue-at - \
    --output "${PARTIAL_PATH}" \
    "${MODEL_URL}"
  mv "${PARTIAL_PATH}" "${MODEL_PATH}"
fi

ls -lh "${MODEL_PATH}"

if [[ -n "${EXPECTED_SHA256}" ]]; then
  ACTUAL_SHA256="$(shasum -a 256 "${MODEL_PATH}" | awk '{print $1}')"
  if [[ "${ACTUAL_SHA256}" != "${EXPECTED_SHA256}" ]]; then
    echo "SHA-256 mismatch for ${MODEL_PATH}" >&2
    echo "expected: ${EXPECTED_SHA256}" >&2
    echo "actual:   ${ACTUAL_SHA256}" >&2
    exit 1
  fi
  echo "SHA-256 verified: ${ACTUAL_SHA256}"
fi

cat <<EOF

Qwen3.8 is ready for llama.cpp．

Start the Wails code backend:
  ./scripts/start_llama_code.sh

Start the coding-agent PoC after the backend is ready:
  ./scripts/run_cli.sh agent "Inspect the repository and summarize its test strategy．" --read-only
EOF
