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
  karte_runtime_pid_matches_executable "${pid}" "${executable}"
}

karte_runtime_pid_matches_executable() {
  local pid=$1
  local executable=$2
  local command
  command="$(ps -ww -p "${pid}" -o command= 2>/dev/null || true)"
  if [[ -n "${command}" ]]; then
    [[ "${command}" == "${executable}" || "${command}" == "${executable} "* ]]
    return
  fi
  if command -v lsof >/dev/null 2>&1; then
    while IFS= read -r command; do
      if [[ "${command}" == "n${executable}" ]]; then
        return 0
      fi
    done < <(lsof -a -p "${pid}" -d txt -Fn 2>/dev/null)
  fi
  return 1
}

karte_runtime_pid_from_data_dir() {
  local data_dir=$1
  local executable=$2
  local expected_pid=${3:-}
  local marker="${data_dir}/.mdsys/runtime/karte.pid"
  local pid

  [[ -f "${marker}" ]] || return 1
  IFS= read -r pid < "${marker}" || return 1
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  if [[ -n "${expected_pid}" ]]; then
    [[ "${pid}" == "${expected_pid}" ]] || return 1
    kill -0 "${pid}" 2>/dev/null || return 1
  elif ! karte_runtime_pid_matches_executable "${pid}" "${executable}"; then
    return 1
  fi
  printf '%s\n' "${pid}"
}

persist_karte_runtime_data_dir() {
  local app_placed_dir=$1
  local data_dir=$2
  local config_path="${app_placed_dir}/.karte-data-dir"
  local temp_path="${config_path}.tmp.$$"
  local persisted
  local extra

  if [[ "${data_dir}" != /* || "${data_dir}" == *$'\n'* || "${data_dir}" == *$'\r'* || ! -d "${data_dir}" ]]; then
    printf 'Refusing invalid Karte data directory pointer．\n' >&2
    return 1
  fi

  mkdir -p "${app_placed_dir}"
  printf '%s\n' "${data_dir}" > "${temp_path}"
  mv -f "${temp_path}" "${config_path}"
  IFS= read -r persisted < "${config_path}" || return 1
  extra=""
  IFS= read -r extra < <(sed -n '2p' "${config_path}") || true
  if [[ "${persisted}" != "${data_dir}" || -n "${extra}" ]]; then
    printf 'Karte data directory pointer verification failed．\n' >&2
    return 1
  fi
}

karte_runtime_find_process_pid() {
  local executable=$1
  local candidate_pid
  local candidate_command

  while read -r candidate_pid candidate_command; do
    if [[ "${candidate_pid}" =~ ^[0-9]+$ ]] &&
      [[ "${candidate_command}" == "${executable}" || "${candidate_command}" == "${executable} "* ]]; then
      printf '%s\n' "${candidate_pid}"
      return 0
    fi
  done < <(ps -ww -axo pid=,command= 2>/dev/null)
  return 1
}

start_bundled_karte_runtime() {
  local runtime_root=$1
  local executable
  local app_bundle=""
  local packaged_app_bundle=""
  local pid_file
  local log_file
  local pid
  local marker_pid

  if [[ "${EPHY_START_KARTE:-1}" == "0" ]]; then
    return 0
  fi
  if ! executable="$(resolve_karte_runtime_executable "${runtime_root}")"; then
    return 0
  fi

  KARTE_DATA_DIR="${KARTE_DATA_DIR:-${runtime_root}/data/runtime/karte-data}"
  mkdir -p "${KARTE_DATA_DIR}/content" "${runtime_root}/data/runtime/pids" "${runtime_root}/data/runtime/logs"
  KARTE_DATA_DIR="$(cd "${KARTE_DATA_DIR}" && pwd -P)"
  export KARTE_DATA_DIR
  pid_file="${runtime_root}/data/runtime/pids/karte.pid"
  log_file="${runtime_root}/data/runtime/logs/karte.log"
  if [[ "${executable}" == */Contents/MacOS/karte ]]; then
    packaged_app_bundle="${executable%/Contents/MacOS/karte}"
    persist_karte_runtime_data_dir "${packaged_app_bundle%/*}" "${KARTE_DATA_DIR}"
    if [[ "$(uname -s)" == "Darwin" && "${EPHY_KARTE_LAUNCH_MODE:-auto}" != "direct" ]]; then
      app_bundle="${packaged_app_bundle}"
    fi
  fi

  if pid="$(karte_runtime_pid_from_data_dir "${KARTE_DATA_DIR}" "${executable}" || true)"; [[ "${pid}" =~ ^[0-9]+$ ]]; then
    printf '%s\n' "${pid}" > "${pid_file}"
    return 0
  fi

  if [[ -n "${app_bundle}" ]]; then
    open -n --env "KARTE_DATA_DIR=${KARTE_DATA_DIR}" "${app_bundle}" >>"${log_file}" 2>&1
    pid=""
    for _ in {1..100}; do
      pid="$(karte_runtime_pid_from_data_dir "${KARTE_DATA_DIR}" "${executable}" || true)"
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
    marker_pid=""
    for _ in {1..100}; do
      marker_pid="$(karte_runtime_pid_from_data_dir "${KARTE_DATA_DIR}" "${executable}" "${pid}" || true)"
      [[ "${marker_pid}" == "${pid}" ]] && break
      if ! kill -0 "${pid}" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if [[ "${marker_pid}" != "${pid}" ]]; then
      kill "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
      printf 'Bundled Karte did not publish its data-root identity．See %s\n' "${log_file}" >&2
      return 1
    fi
  fi
  printf '%s\n' "${pid}" > "${pid_file}"
  sleep 1
  if ! kill -0 "${pid}" 2>/dev/null; then
    printf 'Bundled Karte failed to start．See %s\n' "${log_file}" >&2
    return 1
  fi
  printf 'Started bundled Karte pid=%s data=%s\n' "${pid}" "${KARTE_DATA_DIR}"
}
