#!/bin/sh
# This workstation's isolated verification layout．See README for portable setup．
set -eu
runtime_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
project_root=$(CDPATH= cd -- "$runtime_root/../.." && pwd)
cd "$runtime_root"
mode=${1:-combined}
case "$mode" in
  baseline|selected) speech_python="$project_root/local-runtimes/irodori/Irodori-TTS-Server/.venv/bin/python" ;;
  audio|combined) speech_python="$project_root/local-runtimes/reuse-core/venv/bin/python" ;;
  *) printf '%s\n' 'Use baseline，selected，audio or combined．' >&2; exit 2 ;;
esac
exec "$project_root/local-runtimes/reuse-core/selected-venv/bin/python" -m scripts.reuse_core.launch \
  --mode "$mode" --speech-python "$speech_python" \
  --config "$project_root/local-data/reuse-core/speech-config.json" \
  --binary "$project_root/local-runtimes/reuse-core/ephy-reuse" \
  --state "$project_root/local-data/reuse-core/app-$mode"
