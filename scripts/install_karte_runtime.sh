#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE=${1:?usage: scripts/install_karte_runtime.sh KARTE_APP_OR_ZIP}
DESTINATION_ROOT="${EPHY_RUNTIME_ROOT}/data/runtime/karte"
DESTINATION_APP="${DESTINATION_ROOT}/Karte.app"
STAGING_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ephy-karte-install.XXXXXX")"

cleanup() {
  rm -rf -- "${STAGING_ROOT}"
}
trap cleanup EXIT

if [[ -e "${DESTINATION_APP}" ]]; then
  echo "Bundled Karte already exists at ${DESTINATION_APP}．Move it aside before installing a replacement．" >&2
  exit 1
fi

SOURCE_APP=""
if [[ -d "${SOURCE}" && "${SOURCE}" == *.app ]]; then
  SOURCE_APP="${SOURCE}"
elif [[ -f "${SOURCE}" && "${SOURCE}" == *.zip ]]; then
  ditto -x -k "${SOURCE}" "${STAGING_ROOT}/unpacked"
  SOURCE_APP="$(find "${STAGING_ROOT}/unpacked" -maxdepth 4 -type d -name Karte.app -print -quit)"
else
  echo "Karte source must be a Karte.app bundle or zip artifact．" >&2
  exit 1
fi

if [[ -z "${SOURCE_APP}" ]]; then
  echo "Karte.app was not found in ${SOURCE}．" >&2
  exit 1
fi

SOURCE_EXECUTABLE="${SOURCE_APP}/Contents/MacOS/karte"
if [[ ! -x "${SOURCE_EXECUTABLE}" ]]; then
  echo "Karte executable is missing from ${SOURCE_APP}．" >&2
  exit 1
fi
if [[ "$(uname -s)" == "Darwin" ]]; then
  codesign --verify --deep --strict --verbose=2 "${SOURCE_APP}"
  if ! file "${SOURCE_EXECUTABLE}" | grep -q "arm64"; then
    echo "Karte artifact is not an Apple Silicon executable．" >&2
    exit 1
  fi
fi

mkdir -p "${DESTINATION_ROOT}"
ditto "${SOURCE_APP}" "${DESTINATION_APP}"
chmod +x "${DESTINATION_APP}/Contents/MacOS/karte"
if [[ "$(uname -s)" == "Darwin" ]]; then
  xattr -cr "${DESTINATION_APP}"
  # File Provider volumes can synthesize FinderInfo after it is removed．The
  # source was checked with --strict above，so verify the copied code seal here．
  codesign --verify --deep --verbose=2 "${DESTINATION_APP}"
fi

printf 'Installed bundled Karte at %s\n' "${DESTINATION_APP}"
