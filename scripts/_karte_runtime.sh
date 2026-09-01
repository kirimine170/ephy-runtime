#!/usr/bin/env bash

# This file is sourced by launchers and intentionally does not change shell options．

resolve_karte_runtime_executable() {
  local runtime_root=$1
  local candidate
  local -a candidates=()

  if [[ -n "${EPHY_KARTE_EXECUTABLE:-}" ]]; then
    candidates+=("${EPHY_KARTE_EXECUTABLE}")
  fi
  candidates+=(
    "${runtime_root}/data/runtime/karte/Karte.app/Contents/MacOS/karte"
    "${runtime_root}/data/runtime/karte/bin/karte"
  )

  for candidate in "${candidates[@]}"; do
    if [[ -x "${candidate}" && ! -L "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

karte_runtime_pid_is_live() {
  local pid_file=$1
  local executable=$2
  local pid
  local command

  [[ -f "${pid_file}" ]] || return 1
  IFS= read -r pid < "${pid_file}" || return 1
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null || return 1
  command="$(ps -ww -p "${pid}" -o command= 2>/dev/null || true)"
  if [[ -n "${command}" ]]; then
    [[ "${command}" == *"${executable}"* ]]
    return
  fi
  return 0
}

start_bundled_karte_runtime() {
  local runtime_root=$1
  local executable
  local app_bundle=""
  local process_identity
  local pid_file
  local log_file
  local pid

  if [[ "${EPHY_START_KARTE:-1}" == "0" ]]; then
    return 0
  fi
  if ! executable="$(resolve_karte_runtime_executable "${runtime_root}")"; then
    return 0
  fi

  export KARTE_DATA_DIR="${KARTE_DATA_DIR:-${runtime_root}/data/runtime/karte-data}"
  mkdir -p "${KARTE_DATA_DIR}/content" "${runtime_root}/data/runtime/pids" "${runtime_root}/data/runtime/logs"
  pid_file="${runtime_root}/data/runtime/pids/karte.pid"
  log_file="${runtime_root}/data/runtime/logs/karte.log"
  process_identity="${executable}"
  if [[ "$(uname -s)" == "Darwin" && "${EPHY_KARTE_LAUNCH_MODE:-auto}" != "direct" && "${executable}" == */Contents/MacOS/karte ]]; then
    app_bundle="${executable%/Contents/MacOS/karte}"
  fi

  if karte_runtime_pid_is_live "${pid_file}" "${process_identity}"; then
    return 0
  fi

  if [[ -n "${app_bundle}" ]]; then
    open --env "KARTE_DATA_DIR=${KARTE_DATA_DIR}" "${app_bundle}" >>"${log_file}" 2>&1
    pid=""
    for _ in {1..50}; do
      IFS= read -r pid < <(pgrep -f -x "${executable}" 2>/dev/null || true)
      [[ "${pid}" =~ ^[0-9]+$ ]] && break
      sleep 0.1
    done
    if [[ ! "${pid}" =~ ^[0-9]+$ ]]; then
      printf 'Bundled Karte failed to report a process．See %s\n' "${log_file}" >&2
      return 1
    fi
  else
    nohup env KARTE_DATA_DIR="${KARTE_DATA_DIR}" "${executable}" >>"${log_file}" 2>&1 &
    pid=$!
  fi
  printf '%s\n' "${pid}" > "${pid_file}"
  sleep 1
  if ! kill -0 "${pid}" 2>/dev/null; then
    printf 'Bundled Karte failed to start．See %s\n' "${log_file}" >&2
    return 1
  fi
  printf 'Started bundled Karte pid=%s data=%s\n' "${pid}" "${KARTE_DATA_DIR}"
}
