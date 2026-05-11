#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-$(cd "${DIR}/../../../.." && pwd)}"
DOCKERD_LOG="${DOCKERD_LOG:-/tmp/claw-dockerd.log}"

activate_host_go() {
  local host_goroot="${HOST_GOROOT:-}"
  local host_gomodcache="${HOST_GOMODCACHE:-}"
  if [[ -n "${host_goroot}" && -x "${host_goroot}/bin/go" ]]; then
    export GOROOT="${host_goroot}"
    export PATH="${host_goroot}/bin:${PATH}"
    export GOTOOLCHAIN=local
  fi
  if [[ -n "${host_gomodcache}" && -d "${host_gomodcache}" ]]; then
    export GOMODCACHE="${host_gomodcache}"
  fi
  export GOCACHE="${GOCACHE:-/tmp/go-build}"
  mkdir -p "${GOCACHE}"
}

start_dind() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi

  dockerd --host=unix:///var/run/docker.sock >"${DOCKERD_LOG}" 2>&1 &
  DOCKERD_PID=$!
  export DOCKERD_PID

  for _ in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  echo "dockerd failed to become ready, see ${DOCKERD_LOG}" >&2
  return 1
}

load_runtime_image() {
  local image_tag="${IMAGE_TAG:-claw-runtime:node24-python}"
  local image_tar="${RUNTIME_IMAGE_TAR:-}"

  if docker image inspect "${image_tag}" >/dev/null 2>&1; then
    return 0
  fi
  if [[ -z "${image_tar}" || ! -f "${image_tar}" ]]; then
    echo "runtime image ${image_tag} not present and RUNTIME_IMAGE_TAR is unavailable" >&2
    return 1
  fi

  docker load -i "${image_tar}" >/dev/null
}

cleanup() {
  if [[ -n "${DOCKERD_PID:-}" ]]; then
    kill "${DOCKERD_PID}" >/dev/null 2>&1 || true
    wait "${DOCKERD_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

start_dind
load_runtime_image
activate_host_go

cd "${DIR}"
EXPECTED_HOST_PUBLISHED_INTERNAL_SERVICE_REACHABLE=false ./test_demo.sh
./network_matrix.sh
ROOT="${ROOT}" ./sandbox_go_probe.sh
ROOT="${ROOT}" ./sandbox_go_network_strong_probe.sh
ROOT="${ROOT}" ./sandbox_go_private_service_bypass_probe.sh
ROOT="${ROOT}" ./sandbox_go_process_tree_inheritance_probe.sh
ROOT="${ROOT}" ./same_container_control_plane_risk_probe.sh
ROOT="${ROOT}" ./same_container_control_plane_mitigated_probe.sh
