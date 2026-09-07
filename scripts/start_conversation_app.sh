#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(CDPATH= builtin cd -- "$(dirname "${BASH_SOURCE[0]}")/.." >/dev/null && builtin pwd)"
source "${EPHY_RUNTIME_ROOT}/scripts/_karte_runtime.sh"
EPHY_DESKTOP_APP="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime.app"
EPHY_DESKTOP_EXECUTABLE="${EPHY_DESKTOP_APP}/Contents/MacOS/ephy-runtime"
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  EPHY_DESKTOP_APP=""
  EPHY_DESKTOP_EXECUTABLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime"
fi
if [[ ! -x "${EPHY_DESKTOP_EXECUTABLE}" ]]; then
  echo "Desktop app is not built．Run 'bash scripts/build_conversation_app.sh' or 'wails build' first．" >&2
  exit 1
fi
builtin cd -- "${EPHY_RUNTIME_ROOT}" >/dev/null
start_bundled_karte_runtime "${EPHY_RUNTIME_ROOT}"
if [[ "$(uname -s)" == "Darwin" && -n "${EPHY_DESKTOP_APP}" ]]; then
  # LaunchServices makes the app bundle the responsible TCC process．Running
  # Contents/MacOS/ephy-runtime directly attributes Speech permission to the
  # invoking terminal or automation host，whose plist we do not control．
  exec open -W -n --env EPHY_START_CONVERSATION=1 "${EPHY_DESKTOP_APP}" --args "$@"
fi
export EPHY_START_CONVERSATION=1
exec "${EPHY_DESKTOP_EXECUTABLE}" "$@"
