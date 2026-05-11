#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-$(cd "${DIR}/../../../.." && pwd)}"
PKG="${ROOT}/backend/core/service/aisvr/internal/domain/runtime"

if [[ -n "${HOST_GOROOT:-}" && -x "${HOST_GOROOT}/bin/go" ]]; then
  export GOROOT="${HOST_GOROOT}"
  export PATH="${HOST_GOROOT}/bin:${PATH}"
  export GOTOOLCHAIN=local
fi
if [[ -n "${HOST_GOMODCACHE:-}" && -d "${HOST_GOMODCACHE}" ]]; then
  export GOMODCACHE="${HOST_GOMODCACHE}"
fi
export GOCACHE="${GOCACHE:-/tmp/go-build}"
mkdir -p "${GOCACHE}"

cd "${PKG}"
go test -run 'TestCheckCgroupAvailable|TestCheckNetworkCapabilities|TestHasCgroupV2|TestNewSandboxNetwork' -v
