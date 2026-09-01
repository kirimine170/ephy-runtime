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
  install -m 0755 "${BINARY_PATH}" "${APP_BINARY}"
  touch "${APP_BUNDLE}"
  echo "Updated ${APP_BUNDLE}"
fi
