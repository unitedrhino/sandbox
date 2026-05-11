#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
PORT="${PORT:-18080}"
WORKDIR="$(mktemp -d /tmp/claw-smoke-XXXXXX)"
CONTROLDIR="$(mktemp -d /tmp/claw-control-XXXXXX)"
LOG="${LOG:-/tmp/claw-smoke.log}"

cleanup() {
  if [[ -f /tmp/claw-smoke.pid ]]; then
    kill "$(cat /tmp/claw-smoke.pid)" >/dev/null 2>&1 || true
    wait "$(cat /tmp/claw-smoke.pid)" >/dev/null 2>&1 || true
    rm -f /tmp/claw-smoke.pid
  fi
  rm -rf "${WORKDIR}" "${CONTROLDIR}"
}
trap cleanup EXIT

cd "${ROOT}"
go build -o claw .

CLAW_LISTEN_ADDR=":${PORT}" \
CLAW_RUNTIME_ID="rt-smoke" \
CLAW_TENANT_CODE="t1" \
CLAW_CLONE_ID="c1" \
CLAW_CLONE_KEY="clone-a" \
CLAW_WORKSPACE="${WORKDIR}" \
CLAW_CONTROL_DIR="${CONTROLDIR}" \
./claw >"${LOG}" 2>&1 &
echo $! > /tmp/claw-smoke.pid

for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

echo "=== healthz ==="
curl -s "http://127.0.0.1:${PORT}/healthz"
echo

echo "=== start ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start"
echo

echo "=== exec ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","printf smoke-ok"],"timeoutSeconds":2}'
echo

echo "=== status ==="
curl -s "http://127.0.0.1:${PORT}/runtime/status"
echo

echo "=== skills ==="
curl -s "http://127.0.0.1:${PORT}/runtime/skills"
echo

echo "=== skill detail ==="
curl -s "http://127.0.0.1:${PORT}/runtime/skills/ur-api"
echo
