#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export EPHY_START_CONVERSATION=1
exec "${SCRIPT_DIR}/start_wails.sh" "$@"
