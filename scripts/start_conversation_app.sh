#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EPHY_DESKTOP_EXECUTABLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime.app/Contents/MacOS/ephy-runtime"
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  EPHY_DESKTOP_EXECUTABLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime"
fi
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  echo "Desktop app is not built．Run 'bash scripts/build_conversation_app.sh' or 'wails build' first．" >&2
  exit 1
fi
cd "${EPHY_RUNTIME_ROOT}"
export EPHY_START_CONVERSATION=1
exec "${EPHY_DESKTOP_EXECUTABLE}" "$@"
