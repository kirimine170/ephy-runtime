#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Local Apple Speech provider requires macOS．" >&2
  exit 1
fi

ASR_APP="${EPHY_RUNTIME_ROOT}/bin/EphyASR.app"
EPHY_VOICE_BUILD_CACHE="$(mktemp -d "${TMPDIR:-/tmp}/ephy-voice-build.XXXXXX")"
trap 'rm -rf "${EPHY_VOICE_BUILD_CACHE}"' EXIT
STAGED_ASR_APP="${EPHY_VOICE_BUILD_CACHE}/EphyASR.app"
ASR_CONTENTS="${STAGED_ASR_APP}/Contents"
ASR_EXECUTABLE="${ASR_CONTENTS}/MacOS/ephy-asr"
mkdir -p "${ASR_CONTENTS}/MacOS"
install -m 0644 "${EPHY_RUNTIME_ROOT}/desktop/voice/Info.plist" "${ASR_CONTENTS}/Info.plist"
# Speech recognition is TCC-protected．The bundle provides a stable identity，
# while the embedded plist covers direct child-process execution with pipes．
xcrun swiftc \
  -O \
  -module-cache-path "${EPHY_VOICE_BUILD_CACHE}" \
  -framework Speech \
  -framework AVFoundation \
  -Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist \
  -Xlinker "${EPHY_RUNTIME_ROOT}/desktop/voice/Info.plist" \
  "${EPHY_RUNTIME_ROOT}/desktop/voice/EphyASR.swift" \
  -o "${ASR_EXECUTABLE}"
# Sign in staging first，then clear File Provider metadata and reseal the final
# bundle after copying．FinderInfo invalidates strict verification and can make
# LaunchServices/TCC resolve a stale bundle record．
codesign --force --sign - --identifier jp.ephy.runtime.asr "${STAGED_ASR_APP}"
codesign --verify --strict "${STAGED_ASR_APP}"
rm -rf -- "${ASR_APP}"
mkdir -p "${EPHY_RUNTIME_ROOT}/bin"
cp -R -X "${STAGED_ASR_APP}" "${ASR_APP}"
xattr -cr "${ASR_APP}"
xattr -d com.apple.FinderInfo "${ASR_APP}" 2>/dev/null || true
codesign --force --deep --sign - --identifier jp.ephy.runtime.asr "${ASR_APP}"
codesign --verify --strict --deep "${ASR_APP}"
rm -f -- "${EPHY_RUNTIME_ROOT}/bin/ephy-asr"
echo "Built ${ASR_APP}"
