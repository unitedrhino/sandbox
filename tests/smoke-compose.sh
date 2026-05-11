#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
REPO_ROOT="$(cd "${ROOT}/../.." && pwd)"

PORT="${PORT:-18093}"
PROJECT_NAME="${PROJECT_NAME:-claw-compose-smoke}"
NETWORK_NAME="${NETWORK_NAME:-claw-compose-smoke-net}"
TENANT_CODE="${TENANT_CODE:-t1}"
CLONE_ID="${CLONE_ID:-c1}"
CLONE_KEY="${CLONE_KEY:-clone-a}"
TEMP_ROOT="$(mktemp -d "${REPO_ROOT}/.temp/claw-compose-smoke-XXXXXX")"
WORKSPACE_DIR="${TEMP_ROOT}/workspace"
CONTROL_DIR="${TEMP_ROOT}/control"
SKILLS_STATE_DIR="${TEMP_ROOT}/skills-state"

cleanup() {
  (
    cd "${ROOT}"
    CONTAINER_ID="$(docker compose -p "${PROJECT_NAME}" ps -q claw 2>/dev/null || true)"
    if [[ -n "${CONTAINER_ID}" ]]; then
      docker exec -u 0 "${CONTAINER_ID}" bash -lc "chmod -R 0777 /workspace >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
    elif [[ -d "${WORKSPACE_DIR}" ]]; then
      docker run --rm \
        -v "${WORKSPACE_DIR}:/workspace" \
        --entrypoint /bin/bash \
        claw:dev \
        -lc "chmod -R 0777 /workspace >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
    fi
    CLAW_PORT="${PORT}" \
    CLAW_DOCKER_NETWORK="${NETWORK_NAME}" \
    CLAW_CLONE_ID="${CLONE_ID}" \
    CLAW_WORKSPACE_DIR="${WORKSPACE_DIR}" \
    CLAW_CONTROL_DIR_HOST="${CONTROL_DIR}" \
    CLAW_SKILLS_STATE_DIR="${SKILLS_STATE_DIR}" \
    docker compose -p "${PROJECT_NAME}" down -v --remove-orphans >/dev/null 2>&1 || true
  )
  docker network rm "${NETWORK_NAME}" >/dev/null 2>&1 || true
  rm -rf "${TEMP_ROOT}"
}
trap cleanup EXIT

mkdir -p "${WORKSPACE_DIR}" "${CONTROL_DIR}" "${SKILLS_STATE_DIR}"
chmod 0777 "${WORKSPACE_DIR}" "${CONTROL_DIR}" "${SKILLS_STATE_DIR}"

cd "${ROOT}"

CLAW_PORT="${PORT}" \
CLAW_DOCKER_NETWORK="${NETWORK_NAME}" \
CLAW_TENANT_CODE="${TENANT_CODE}" \
CLAW_CLONE_ID="${CLONE_ID}" \
CLAW_CLONE_KEY="${CLONE_KEY}" \
CLAW_WORKSPACE_DIR="${WORKSPACE_DIR}" \
CLAW_CONTROL_DIR_HOST="${CONTROL_DIR}" \
CLAW_SKILLS_STATE_DIR="${SKILLS_STATE_DIR}" \
docker compose -p "${PROJECT_NAME}" up -d --build

for _ in $(seq 1 120); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "=== compose ps ==="
docker compose -p "${PROJECT_NAME}" ps
echo

echo "=== healthz ==="
curl -s "http://127.0.0.1:${PORT}/healthz"
echo

echo "=== runtime start ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start"
echo

echo "=== runtime status ==="
STATUS_JSON="$(curl -s "http://127.0.0.1:${PORT}/runtime/status")"
printf '%s\n' "${STATUS_JSON}"
python3 - <<'PY' "${STATUS_JSON}" "${TENANT_CODE}" "${CLONE_KEY}" "${CLONE_ID}"
import json
import sys

status = json.loads(sys.argv[1])
tenant = sys.argv[2]
clone = sys.argv[3]
clone_id = sys.argv[4]
expected = f"/workspace/{tenant}/{clone}/{clone_id}/work"
actual = status.get("workspace")
if actual != expected:
    raise SystemExit(f"expected workspace {expected}, got {actual}")
PY
echo

CONTAINER_NAME="$(docker compose -p "${PROJECT_NAME}" ps -q claw)"
if [[ -z "${CONTAINER_NAME}" ]]; then
  echo "compose did not create claw container" >&2
  exit 1
fi

ACTUAL_NETWORKS="$(docker inspect "${CONTAINER_NAME}" --format '{{json .NetworkSettings.Networks}}')"
echo "=== attached networks ==="
python3 - <<'PY' "${ACTUAL_NETWORKS}" "${NETWORK_NAME}"
import json
import sys

networks = json.loads(sys.argv[1])
expected = sys.argv[2]
keys = sorted(networks.keys())
print(json.dumps({"attached_networks": keys}, ensure_ascii=False))
if keys != [expected]:
    raise SystemExit(f"expected only [{expected}], got {keys}")
PY
echo

echo "=== compose down ==="
CLAW_PORT="${PORT}" \
CLAW_DOCKER_NETWORK="${NETWORK_NAME}" \
CLAW_CLONE_ID="${CLONE_ID}" \
CLAW_WORKSPACE_DIR="${WORKSPACE_DIR}" \
CLAW_CONTROL_DIR_HOST="${CONTROL_DIR}" \
CLAW_SKILLS_STATE_DIR="${SKILLS_STATE_DIR}" \
docker compose -p "${PROJECT_NAME}" down -v --remove-orphans
echo

echo "=== network removed ==="
if docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1; then
  echo "network still exists: ${NETWORK_NAME}" >&2
  exit 1
fi
echo "removed ${NETWORK_NAME}"
