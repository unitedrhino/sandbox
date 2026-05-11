#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${DIR}/../lib/docker-network.sh"
IMAGE_TAG="${IMAGE_TAG:-claw-runtime:node24-python}"
CONTAINER_NAME="${CONTAINER_NAME:-claw-control-plane-risk}"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"

WORK_ROOT="$(mktemp -d /tmp/claw-control-plane-risk-XXXXXX)"
WORKSPACE_DIR="${WORK_ROOT}/workspace"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  chmod -R u+w "${WORK_ROOT}" >/dev/null 2>&1 || true
  rm -rf "${WORK_ROOT}"
}
trap cleanup EXIT

mkdir -p "${WORKSPACE_DIR}/control"
chmod -R 0777 "${WORKSPACE_DIR}"

if ! docker image inspect "${IMAGE_TAG}" >/dev/null 2>&1; then
  docker build -t "${IMAGE_TAG}" "${DIR}" >/dev/null
fi

ensure_claw_docker_network "${NETWORK_NAME}"
docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  --mount "type=bind,src=${WORKSPACE_DIR},dst=/workspace" \
  "${IMAGE_TAG}" >/dev/null

docker exec "${CONTAINER_NAME}" bash -lc "echo 'main-control-file' > /workspace/control/claw-main.txt"
docker exec -d "${CONTAINER_NAME}" python3 -c "import os,time; open('/workspace/control/main.pid','w').write(str(os.getpid())); time.sleep(3600)"

for _ in $(seq 1 20); do
  if docker exec "${CONTAINER_NAME}" bash -lc "test -f /workspace/control/main.pid" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

MAIN_PID="$(docker exec "${CONTAINER_NAME}" bash -lc "cat /workspace/control/main.pid")"

same_user_kill_succeeded=false
if docker exec "${CONTAINER_NAME}" bash -lc "kill -KILL ${MAIN_PID}" >/dev/null 2>&1; then
  sleep 0.5
  if ! docker exec "${CONTAINER_NAME}" bash -lc "kill -0 ${MAIN_PID}" >/dev/null 2>&1; then
    same_user_kill_succeeded=true
  fi
fi

same_user_delete_succeeded=false
if docker exec "${CONTAINER_NAME}" bash -lc "rm -f /workspace/control/claw-main.txt" >/dev/null 2>&1; then
  if ! docker exec "${CONTAINER_NAME}" bash -lc "test -f /workspace/control/claw-main.txt" >/dev/null 2>&1; then
    same_user_delete_succeeded=true
  fi
fi

python3 - <<'PY' "${same_user_kill_succeeded}" "${same_user_delete_succeeded}"
import json
import sys

result = {
    "sameUserKillSucceeded": sys.argv[1] == "true",
    "sameUserDeleteSucceeded": sys.argv[2] == "true",
}
print(json.dumps(result, ensure_ascii=False))
if not result["sameUserKillSucceeded"]:
    raise SystemExit("expected same-user kill to succeed in current same-container model")
if not result["sameUserDeleteSucceeded"]:
    raise SystemExit("expected same-user delete to succeed in current same-container model")
print("same-container control-plane risk verified")
PY
