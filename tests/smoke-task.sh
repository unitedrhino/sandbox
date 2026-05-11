#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
PORT="${PORT:-18085}"
WORKDIR="$(mktemp -d /tmp/claw-task-smoke-XXXXXX)"
CONTROLDIR="$(mktemp -d /tmp/claw-task-control-XXXXXX)"
LOG="${LOG:-/tmp/claw-task-smoke.log}"

cleanup() {
  if [[ -f /tmp/claw-task-smoke.pid ]]; then
    kill "$(cat /tmp/claw-task-smoke.pid)" >/dev/null 2>&1 || true
    wait "$(cat /tmp/claw-task-smoke.pid)" >/dev/null 2>&1 || true
    rm -f /tmp/claw-task-smoke.pid
  fi
  rm -rf "${WORKDIR}" "${CONTROLDIR}"
}
trap cleanup EXIT

cd "${ROOT}"
go build -o claw .

CLAW_LISTEN_ADDR=":${PORT}" \
CLAW_RUNTIME_ID="rt-task" \
CLAW_TENANT_CODE="t1" \
CLAW_CLONE_ID="c1" \
CLAW_CLONE_KEY="clone-a" \
CLAW_WORKSPACE="${WORKDIR}" \
CLAW_CONTROL_DIR="${CONTROLDIR}" \
./claw >"${LOG}" 2>&1 &
echo $! > /tmp/claw-task-smoke.pid

for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start" >/dev/null

TASK_JSON="$(curl -s -X POST "http://127.0.0.1:${PORT}/runtime/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","printf task-state-ok"],"timeoutSeconds":3}')"

TASK_ID="$(python3 - <<'PY' "${TASK_JSON}"
import json, sys
print(json.loads(sys.argv[1])["id"])
PY
)"

echo "=== task created ==="
printf '%s\n' "${TASK_JSON}"

sleep 0.2

echo "=== task get ==="
curl -s "http://127.0.0.1:${PORT}/runtime/tasks/${TASK_ID}"
echo

echo "=== task list ==="
curl -s "http://127.0.0.1:${PORT}/runtime/tasks"
echo

CANCEL_JSON="$(curl -s -X POST "http://127.0.0.1:${PORT}/runtime/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","sleep 10"],"timeoutSeconds":20}')"

CANCEL_TASK_ID="$(python3 - <<'PY' "${CANCEL_JSON}"
import json, sys
print(json.loads(sys.argv[1])["id"])
PY
)"

sleep 0.2

echo "=== task cancel ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/tasks/${CANCEL_TASK_ID}/cancel"
echo

sleep 0.2

echo "=== canceled task get ==="
curl -s "http://127.0.0.1:${PORT}/runtime/tasks/${CANCEL_TASK_ID}"
echo
