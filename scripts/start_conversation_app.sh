#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(CDPATH= builtin cd -- "$(dirname "${BASH_SOURCE[0]}")/.." >/dev/null && builtin pwd)"
source "${EPHY_RUNTIME_ROOT}/scripts/_karte_runtime.sh"
EPHY_DESKTOP_EXECUTABLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime.app/Contents/MacOS/ephy-runtime"
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  EPHY_DESKTOP_EXECUTABLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime"
fi
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  echo "Desktop app is not built．Run 'bash scripts/build_conversation_app.sh' or 'wails build' first．" >&2
  exit 1
fi
builtin cd -- "${EPHY_RUNTIME_ROOT}" >/dev/null
start_bundled_karte_runtime "${EPHY_RUNTIME_ROOT}"
export EPHY_START_CONVERSATION=1
exec "${EPHY_DESKTOP_EXECUTABLE}" "$@"
