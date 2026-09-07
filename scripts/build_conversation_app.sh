#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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
  plutil -replace NSMicrophoneUsageDescription -string "録音ボタンを押した間だけ，Ephyとの音声会話にマイクを使います．" "${INFO_PLIST}"
  plutil -replace NSSpeechRecognitionUsageDescription -string "録音した発話を端末内で文字に変換します．" "${INFO_PLIST}"
  install -m 0755 "${BINARY_PATH}" "${APP_BINARY}"
  touch "${APP_BUNDLE}"
  # Keep the bundle identifier stable for TCC．An unsigned Go binary is
  # otherwise attributed as `a.out` even when LaunchServices opens the app．
  xattr -cr "${APP_BUNDLE}"
  codesign --force --deep --sign - --identifier com.wails.ephy-runtime "${APP_BUNDLE}"
  codesign --verify --strict --deep "${APP_BUNDLE}"
  echo "Updated ${APP_BUNDLE}"
fi
