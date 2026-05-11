#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/../../../.." && pwd)"
TEST_IMAGE="${TEST_IMAGE:-claw-security-smoke:dind}"
RUNTIME_IMAGE_TAG="${RUNTIME_IMAGE_TAG:-claw-runtime:node24-python}"
TEMP_DIR="${ROOT}/.temp/claw-security-smoke"
RUNTIME_IMAGE_TAR="${TEMP_DIR}/runtime-image.tar"
HOST_GOROOT="${HOST_GOROOT:-$(go env GOROOT)}"
HOST_GOMODCACHE="${HOST_GOMODCACHE:-$(go env GOPATH)/pkg/mod}"

mkdir -p "${TEMP_DIR}"

if ! docker image inspect "${RUNTIME_IMAGE_TAG}" >/dev/null 2>&1; then
  echo "runtime image not found: ${RUNTIME_IMAGE_TAG}" >&2
  exit 1
fi

docker save -o "${RUNTIME_IMAGE_TAR}" "${RUNTIME_IMAGE_TAG}"

docker build -f "${DIR}/Dockerfile" -t "${TEST_IMAGE}" "${DIR}"

docker run --rm \
  --privileged \
  --network none \
  -e ROOT=/repo \
  -e IMAGE_TAG="${RUNTIME_IMAGE_TAG}" \
  -e RUNTIME_IMAGE_TAR=/runtime/runtime-image.tar \
  -e HOST_GOROOT=/host-go \
  -e HOST_GOMODCACHE=/host-gomodcache \
  -v "${ROOT}:/repo:ro" \
  -v "${RUNTIME_IMAGE_TAR}:/runtime/runtime-image.tar:ro" \
  -v "${HOST_GOROOT}:/host-go:ro" \
  -v "${HOST_GOMODCACHE}:/host-gomodcache:ro" \
  "${TEST_IMAGE}" \
  bash /repo/backend/claw/tests/security-smoke/run_in_docker_suite.sh
