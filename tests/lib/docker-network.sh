#!/usr/bin/env bash
set -euo pipefail

claw_default_network_name() {
  local name="${1:-claw-runtime}"
  printf '%s-net\n' "${name}"
}

ensure_claw_docker_network() {
  local network_name="${1:?network name required}"
  docker network inspect "${network_name}" >/dev/null 2>&1 || docker network create "${network_name}" >/dev/null
}

cleanup_claw_docker_network() {
  local network_name="${1:-}"
  if [[ -z "${network_name}" ]]; then
    return 0
  fi
  docker network rm "${network_name}" >/dev/null 2>&1 || true
}
