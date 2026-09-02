#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${EPHY_RUNTIME_ROOT}/scripts/_karte_runtime.sh"

TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ephy-karte-runtime-test.(path)[v1].XXXXXX")"
FAKE_EXECUTABLE="${TEST_ROOT}/data/runtime/karte/Karte.app/Contents/MacOS/karte"
FAKE_DATA="${TEST_ROOT}/karte-data"
exact_pid=""
karte_pids=()

cleanup() {
  if [[ "${exact_pid:-}" =~ ^[0-9]+$ ]]; then
    kill "${exact_pid}" 2>/dev/null || true
    wait "${exact_pid}" 2>/dev/null || true
  fi
  for pid in "${karte_pids[@]}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
  rm -rf -- "${TEST_ROOT}"
}
trap cleanup EXIT

mkdir -p "$(dirname "${FAKE_EXECUTABLE}")"
printf '#!/usr/bin/env bash\nmkdir -p "$KARTE_DATA_DIR/.mdsys/runtime"\nprintf "%%s\\n" "$$" > "$KARTE_DATA_DIR/.mdsys/runtime/karte.pid"\nprintf "%%s" "$KARTE_DATA_DIR" > "$KARTE_DATA_DIR/observed-data-dir"\nwhile :; do sleep 1; done\n' > "${FAKE_EXECUTABLE}"
chmod +x "${FAKE_EXECUTABLE}"

EXACT_EXECUTABLE="${TEST_ROOT}/Karte (1)[test]"
if ps -ww -axo pid=,command= >/dev/null 2>&1; then
  cp "$(command -v sleep)" "${EXACT_EXECUTABLE}"
  "${EXACT_EXECUTABLE}" 30 &
  exact_pid=$!
  detected_pid="$(karte_runtime_find_process_pid "${EXACT_EXECUTABLE}")"
  kill "${exact_pid}" 2>/dev/null || true
  wait "${exact_pid}" 2>/dev/null || true
  if [[ "${detected_pid}" != "${exact_pid}" ]]; then
    echo "Karte process lookup failed for a path containing regex characters．" >&2
    exit 1
  fi
fi

resolved="$(resolve_karte_runtime_executable "${TEST_ROOT}")"
if [[ "${resolved}" != "${FAKE_EXECUTABLE}" ]]; then
  echo "Bundled Karte executable resolution failed．" >&2
  exit 1
fi

export KARTE_DATA_DIR="${FAKE_DATA}"
export EPHY_KARTE_LAUNCH_MODE=direct
mkdir -p "${TEST_ROOT}/data/runtime/karte"
printf 'content\ndata\nlog\npublic\nthemes\n%s\n' "${FAKE_DATA}" > "${TEST_ROOT}/data/runtime/karte/.karte-data-dir"
start_bundled_karte_runtime "${TEST_ROOT}"
FAKE_DATA="${KARTE_DATA_DIR}"
pid_file="${TEST_ROOT}/data/runtime/pids/karte.pid"
first_pid="$(cat "${pid_file}")"
karte_pids+=("${first_pid}")
for _ in {1..50}; do
  [[ -f "${FAKE_DATA}/observed-data-dir" ]] && break
  sleep 0.1
done
if [[ ! -f "${FAKE_DATA}/observed-data-dir" ]]; then
  echo "Fake Karte did not record its data directory．" >&2
  exit 1
fi
if [[ "$(<"${FAKE_DATA}/observed-data-dir")" != "${FAKE_DATA}" ]]; then
  echo "Fake Karte received an unexpected data directory．" >&2
  exit 1
fi
if [[ "$(<"${TEST_ROOT}/data/runtime/karte/.karte-data-dir")" != "${FAKE_DATA}" ]]; then
  echo "Bundled Karte did not persist its data directory．" >&2
  exit 1
fi

if karte_runtime_pid_matches_executable "${first_pid}" "${FAKE_EXECUTABLE}"; then
  start_bundled_karte_runtime "${TEST_ROOT}"
  if [[ "$(<"${pid_file}")" != "${first_pid}" ]]; then
    echo "Bundled Karte was started twice for one data root．" >&2
    exit 1
  fi
fi

SECOND_DATA="${TEST_ROOT}/karte-data-second"
export KARTE_DATA_DIR="${SECOND_DATA}"
start_bundled_karte_runtime "${TEST_ROOT}"
SECOND_DATA="${KARTE_DATA_DIR}"
second_pid="$(cat "${pid_file}")"
karte_pids+=("${second_pid}")
if [[ "${second_pid}" == "${first_pid}" ]]; then
  echo "Karte process from a different data root was reused．" >&2
  exit 1
fi
if [[ "$(<"${SECOND_DATA}/observed-data-dir")" != "${SECOND_DATA}" ]]; then
  echo "Second Karte received an unexpected data directory．" >&2
  exit 1
fi
if [[ "$(<"${TEST_ROOT}/data/runtime/karte/.karte-data-dir")" != "${SECOND_DATA}" ]]; then
  echo "Bundled Karte did not update its persisted data directory．" >&2
  exit 1
fi

echo "Bundled Karte runtime test passed．"
