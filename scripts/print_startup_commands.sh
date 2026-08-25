#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_runtime_common.sh"

FAST_MODEL_PATH="$(resolve_fast_model_path || true)"
WORK_MODEL_PATH="$(resolve_work_model_path || true)"
CODE_MODEL_PATH="$(resolve_code_model_path || true)"
EMBEDDING_MODEL_PATH="$(resolve_embedding_model_path || true)"

cat <<EOF
cd ${ROOT_DIR}

# Canonical phase1 entrypoint
./scripts/phase1.sh
./scripts/phase1.sh full
./scripts/phase1.sh check
./scripts/phase1.sh commands
./scripts/phase1.sh backend
./scripts/phase1.sh ui
./scripts/phase1.sh restart
./scripts/phase1.sh stop
./scripts/phase1.sh qdrant
./scripts/phase1.sh qdrant-stop
./scripts/phase1.sh qdrant-restart

# Phase1 helper wrappers
./scripts/start_phase1_stack.sh
./scripts/start_phase1_backend.sh
./scripts/start_phase1_ui.sh
./scripts/stop_phase1.sh

# Lower-level consolidated entrypoint
./scripts/workbench.sh check
./scripts/workbench.sh doctor
./scripts/workbench.sh commands
./scripts/workbench.sh start-phase1
./scripts/workbench.sh start-full
./scripts/workbench.sh start-backend
./scripts/workbench.sh start-qdrant
./scripts/workbench.sh stop-qdrant
./scripts/workbench.sh restart-qdrant
./scripts/workbench.sh restart-full
./scripts/workbench.sh stop-full

# Compatibility aliases
./scripts/start_full_feature.sh
./scripts/full_feature.sh
./scripts/run_full_feature.sh
./scripts/start_ephy_runtime.sh
./scripts/start_complete_stack.sh

# Docker-independent phase1 stack: fast/work/code/embedding + local qdrant + gateway + Wails
./scripts/start_phase1_stack.sh

# Full feature entrypoint: resolve model paths, write local overrides, then start llama.cpp backends + embedding + Qdrant + Wails
./scripts/start_full_feature.sh
./scripts/start_complete_stack.sh

# Full feature backends only: llama.cpp x4 + gateway + Qdrant
./scripts/start_backend_stack.sh --with-embedding --with-qdrant

# Default stack only: fast/work/code/embedding/qdrant/gateway + Wails
./scripts/start_full_stack.sh
./scripts/start_backend_stack.sh
./scripts/start_wails.sh

# Skip qdrant explicitly
./scripts/start_backend_stack.sh --without-qdrant

# Skip embedding explicitly (RAG ingest/search then requires a compatible config override)
./scripts/start_backend_stack.sh --without-embedding

# Only write the full-feature local overrides without starting anything
./scripts/apply_full_feature_overrides.sh

# Runtime check
./scripts/check_runtime_setup.sh

# Stop managed backends
./scripts/stop_backend_stack.sh

# Stop full feature stack including Qdrant
./scripts/stop_complete_stack.sh

# Startup note
# ./scripts/phase1.sh is Docker-independent and starts local embedding + Qdrant by default.

# Manual component startup when you want direct control
./scripts/start_llama_fast.sh
./scripts/start_llama_work.sh
./scripts/start_llama_code.sh
./scripts/start_llama_embedding.sh
./scripts/start_qdrant.sh
./scripts/stop_qdrant.sh
./scripts/restart_qdrant.sh
./scripts/start_gateway.sh
./scripts/start_wails.sh

# Qdrant binary lookup order
# QDRANT_BIN, ./bin/qdrant, ./tools/qdrant/qdrant, PATH, /opt/homebrew/bin/qdrant, /usr/local/bin/qdrant

# Optional: watch loop
./scripts/run_cli.sh watch data/docs --project lab --interval 2

# Verified manual llama.cpp commands for this workspace
# Note: use ./llama.cpp/models/... paths, not ./models/...
EOF

print_model_command "${FAST_MODEL_PATH}" 8081 32768 "qwen3-8b"
print_model_command "${WORK_MODEL_PATH}" 8082 32768 "qwen3-30b-a3b"
print_model_command "${CODE_MODEL_PATH}" 8083 32768 "qwen3.8-27b"
print_model_command "${EMBEDDING_MODEL_PATH}" 8090 8192 "qwen3-embedding-0.6b" "--embedding"
