#!/usr/bin/env bash
set -euo pipefail

EPHY_RUNTIME_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${EPHY_RUNTIME_ROOT}/scripts/_karte_runtime.sh"

TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ephy-karte-runtime-test.XXXXXX")"
FAKE_EXECUTABLE="${TEST_ROOT}/data/runtime/karte/Karte.app/Contents/MacOS/karte"
FAKE_DATA="${TEST_ROOT}/karte-data"

cleanup() {
  if [[ -f "${TEST_ROOT}/data/runtime/pids/karte.pid" ]]; then
    IFS= read -r pid < "${TEST_ROOT}/data/runtime/pids/karte.pid" || true
    if [[ "${pid:-}" =~ ^[0-9]+$ ]]; then
      kill "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  fi
  rm -rf -- "${TEST_ROOT}"
}
trap cleanup EXIT

mkdir -p "$(dirname "${FAKE_EXECUTABLE}")"
printf '#!/usr/bin/env bash\nprintf "%%s" "$KARTE_DATA_DIR" > "%s/observed-data-dir"\nsleep 30\n' "${TEST_ROOT}" > "${FAKE_EXECUTABLE}"
chmod +x "${FAKE_EXECUTABLE}"

resolved="$(resolve_karte_runtime_executable "${TEST_ROOT}")"
[[ "${resolved}" == "${FAKE_EXECUTABLE}" ]]

export KARTE_DATA_DIR="${FAKE_DATA}"
export EPHY_KARTE_LAUNCH_MODE=direct
start_bundled_karte_runtime "${TEST_ROOT}"
pid_file="${TEST_ROOT}/data/runtime/pids/karte.pid"
karte_runtime_pid_is_live "${pid_file}" "${FAKE_EXECUTABLE}"
for _ in {1..50}; do
  [[ -f "${TEST_ROOT}/observed-data-dir" ]] && break
  sleep 0.1
done
if [[ ! -f "${TEST_ROOT}/observed-data-dir" ]]; then
  echo "Fake Karte did not record its data directory．" >&2
  exit 1
fi
if [[ "$(<"${TEST_ROOT}/observed-data-dir")" != "${FAKE_DATA}" ]]; then
  echo "Fake Karte received an unexpected data directory．" >&2
  exit 1
fi

first_pid="$(cat "${pid_file}")"
start_bundled_karte_runtime "${TEST_ROOT}"
if [[ "$(<"${pid_file}")" != "${first_pid}" ]]; then
  echo "Bundled Karte was started twice．" >&2
  exit 1
fi

echo "Bundled Karte runtime test passed．"
