#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

EPHY_BUILD_SNAPSHOT="$(mktemp)"
trap 'rm -f "${EPHY_BUILD_SNAPSHOT}"' EXIT
python3 "${EPHY_RUNTIME_ROOT}/scripts/runtime_build_provenance.py" snapshot "${EPHY_BUILD_SNAPSHOT}"

cd "${EPHY_RUNTIME_ROOT}/desktop/frontend"
npm run build

cd "${EPHY_RUNTIME_ROOT}/desktop"
mkdir -p build/bin
BINARY_PATH="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime"
go build \
  -buildvcs=false \
  -tags "desktop,wv2runtime.download,production" \
  -ldflags "-w -s" \
  -o "${BINARY_PATH}" \
  .

echo "Built ${BINARY_PATH}"

if [[ "$(uname -s)" == "Darwin" ]]; then
  APP_BUNDLE="${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime.app"
  APP_BINARY="${APP_BUNDLE}/Contents/MacOS/ephy-runtime"
  INFO_PLIST="${APP_BUNDLE}/Contents/Info.plist"
  mkdir -p "${APP_BUNDLE}/Contents/MacOS" "${APP_BUNDLE}/Contents/Resources"
  if [[ ! -f "${INFO_PLIST}" ]]; then
    plutil -create xml1 "${INFO_PLIST}"
    plutil -insert CFBundlePackageType -string APPL "${INFO_PLIST}"
    plutil -insert CFBundleName -string "Ephy Runtime" "${INFO_PLIST}"
    plutil -insert CFBundleExecutable -string ephy-runtime "${INFO_PLIST}"
    plutil -insert CFBundleIdentifier -string com.wails.ephy-runtime "${INFO_PLIST}"
    plutil -insert CFBundleVersion -string 1.0.0 "${INFO_PLIST}"
    plutil -insert CFBundleShortVersionString -string 1.0.0 "${INFO_PLIST}"
    plutil -insert LSMinimumSystemVersion -string 10.13.0 "${INFO_PLIST}"
    plutil -insert NSHighResolutionCapable -bool true "${INFO_PLIST}"
  fi
  # Wails WebKit capture and the on-device ASR helper are attributed to this app．
  plutil -replace NSMicrophoneUsageDescription -string "音声入力と，有効にしたフィラーの割込み検出にマイクを使います．音声は保存しません．" "${INFO_PLIST}"
  plutil -replace NSSpeechRecognitionUsageDescription -string "録音した発話を端末内で文字に変換します．" "${INFO_PLIST}"
  install -m 0755 "${BINARY_PATH}" "${APP_BINARY}"
  python3 "${EPHY_RUNTIME_ROOT}/scripts/build_recording_control.py" --app "${APP_BUNDLE}"
  if [[ -f "${EPHY_RUNTIME_ROOT}/bin/ephy-whisper" ]]; then
    # Weights remain in the explicitly configured external asset directory．
    mkdir -p "${APP_BUNDLE}/Contents/Helpers" "${APP_BUNDLE}/Contents/Resources/whisper-notices"
    install -m 0755 "${EPHY_RUNTIME_ROOT}/bin/ephy-whisper" "${APP_BUNDLE}/Contents/Helpers/ephy-whisper"
    codesign --verify --strict "${APP_BUNDLE}/Contents/Helpers/ephy-whisper"
    install -m 0644 "${EPHY_RUNTIME_ROOT}/bin/whisper-build-provenance.json" "${APP_BUNDLE}/Contents/Resources/whisper-build-provenance.json"
    cp -R -X "${EPHY_RUNTIME_ROOT}/bin/whisper-notices/." "${APP_BUNDLE}/Contents/Resources/whisper-notices/"
  fi
  touch "${APP_BUNDLE}"
  # Keep the bundle identifier stable for TCC．An unsigned Go binary is
  # otherwise attributed as `a.out` even when LaunchServices opens the app．
  xattr -cr "${APP_BUNDLE}"
  # File Provider can leave root FinderInfo behind even after recursive cleanup．
  # Signing still verifies the complete bundle and fails on other detritus．
  xattr -d com.apple.FinderInfo "${APP_BUNDLE}" 2>/dev/null || true
  # Sign inside out．A forced deep signature would re-sign the ASR helper
  # with the outer identifier and invalidate its recorded build hash．
  codesign --force --sign - --identifier com.wails.ephy-runtime "${APP_BUNDLE}"
  codesign --verify --strict --deep "${APP_BUNDLE}"
  echo "Updated ${APP_BUNDLE}"
fi

EPHY_PROVENANCE_BINARY="${BINARY_PATH}"
if [[ "$(uname -s)" == "Darwin" ]]; then EPHY_PROVENANCE_BINARY="${APP_BINARY}"; fi
python3 "${EPHY_RUNTIME_ROOT}/scripts/runtime_build_provenance.py" finish "${EPHY_BUILD_SNAPSHOT}" \
  --binary "${EPHY_PROVENANCE_BINARY}" --output "${EPHY_RUNTIME_ROOT}/desktop/build/bin/runtime-build-provenance.json"
