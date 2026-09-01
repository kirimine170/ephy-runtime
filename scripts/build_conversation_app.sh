#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${EPHY_RUNTIME_ROOT}/desktop/frontend"
npm run build

cd "${EPHY_RUNTIME_ROOT}/desktop"
mkdir -p build/bin
go build \
  -buildvcs=false \
  -tags "desktop,wv2runtime.download,production" \
  -ldflags "-w -s" \
  -o build/bin/ephy-runtime \
  .

echo "Built ${EPHY_RUNTIME_ROOT}/desktop/build/bin/ephy-runtime"
