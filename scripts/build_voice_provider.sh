#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Local Apple Speech provider requires macOS．" >&2
  exit 1
fi

mkdir -p "${EPHY_RUNTIME_ROOT}/bin"
EPHY_VOICE_BUILD_CACHE="$(mktemp -d "${TMPDIR:-/tmp}/ephy-voice-build.XXXXXX")"
trap 'rm -rf "${EPHY_VOICE_BUILD_CACHE}"' EXIT
# Embed a usage-description plist so the command-line helper has a stable TCC identity．
xcrun swiftc \
  -O \
  -module-cache-path "${EPHY_VOICE_BUILD_CACHE}" \
  -framework Speech \
  -framework AVFoundation \
  -Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist \
  -Xlinker "${EPHY_RUNTIME_ROOT}/desktop/voice/Info.plist" \
  "${EPHY_RUNTIME_ROOT}/desktop/voice/EphyASR.swift" \
  -o "${EPHY_RUNTIME_ROOT}/bin/ephy-asr"
echo "Built ${EPHY_RUNTIME_ROOT}/bin/ephy-asr"
