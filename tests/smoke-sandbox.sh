#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
PORT="${PORT:-18081}"
WORKDIR="$(mktemp -d /tmp/claw-sandbox-smoke-XXXXXX)"
CONTROLDIR="$(mktemp -d /tmp/claw-sandbox-control-XXXXXX)"
LOG="${LOG:-/tmp/claw-sandbox-smoke.log}"

cleanup() {
  if [[ -f /tmp/claw-sandbox-smoke.pid ]]; then
    kill "$(cat /tmp/claw-sandbox-smoke.pid)" >/dev/null 2>&1 || true
    wait "$(cat /tmp/claw-sandbox-smoke.pid)" >/dev/null 2>&1 || true
    rm -f /tmp/claw-sandbox-smoke.pid
  fi
  rm -rf "${WORKDIR}" "${CONTROLDIR}"
}
trap cleanup EXIT

cd "${ROOT}"
go build -o claw .

CLAW_LISTEN_ADDR=":${PORT}" \
CLAW_RUNTIME_ID="rt-sandbox" \
CLAW_TENANT_CODE="t1" \
CLAW_CLONE_ID="c1" \
CLAW_CLONE_KEY="clone-a" \
CLAW_WORKSPACE="${WORKDIR}" \
CLAW_CONTROL_DIR="${CONTROLDIR}" \
CLAW_ENABLE_SANDBOX_NET="true" \
CLAW_SANDBOX_BLOCKED_CIDRS="10.233.1.0/24" \
./claw >"${LOG}" 2>&1 &
echo $! > /tmp/claw-sandbox-smoke.pid

for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

echo "=== start ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start"
echo

echo "=== isolated exec ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","printf isolated-ok"],"timeoutSeconds":3}'
echo

echo "=== private target direct connect (should fail) ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","python3 - <<'\''EOF'\''\nimport socket\ns=socket.socket(); s.settimeout(1)\ns.connect((\"10.233.1.2\",15432))\nEOF"],"timeoutSeconds":3}'
echo
