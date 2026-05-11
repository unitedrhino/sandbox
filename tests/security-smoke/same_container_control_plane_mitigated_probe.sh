#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${DIR}/../lib/docker-network.sh"
IMAGE_TAG="${IMAGE_TAG:-claw-runtime:node24-python}"
CONTAINER_NAME="${CONTAINER_NAME:-claw-control-plane-mitigated}"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"

WORK_ROOT="$(mktemp -d /tmp/claw-control-plane-mitigated-XXXXXX)"
WORKSPACE_DIR="${WORK_ROOT}/workspace"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  chmod -R u+w "${WORK_ROOT}" >/dev/null 2>&1 || true
  rm -rf "${WORK_ROOT}" >/dev/null 2>&1 || sudo -n rm -rf "${WORK_ROOT}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "${WORKSPACE_DIR}/control" "${WORKSPACE_DIR}/task"
chmod 0777 "${WORKSPACE_DIR}/task"

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

# Simulate claw main process on a different UID and a readonly control directory.
docker exec -u 0 "${CONTAINER_NAME}" bash -lc "
  chown root:root /workspace/control &&
  chmod 0555 /workspace/control &&
  echo 'main-control-file' > /workspace/control/claw-main.txt &&
  chmod 0444 /workspace/control/claw-main.txt
"
docker exec -u 0 -d "${CONTAINER_NAME}" python3 -c "import os,time; open('/workspace/control/main.pid','w').write(str(os.getpid())); time.sleep(3600)"

for _ in $(seq 1 20); do
  if docker exec -u 0 "${CONTAINER_NAME}" bash -lc "test -f /workspace/control/main.pid" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

MAIN_PID="$(docker exec -u 0 "${CONTAINER_NAME}" bash -lc "cat /workspace/control/main.pid")"

cross_user_kill_blocked=false
if docker exec "${CONTAINER_NAME}" bash -lc "kill -KILL ${MAIN_PID}" >/tmp/claw-kill.out 2>/tmp/claw-kill.err; then
  cross_user_kill_blocked=false
else
  if docker exec -u 0 "${CONTAINER_NAME}" bash -lc "kill -0 ${MAIN_PID}" >/dev/null 2>&1; then
    cross_user_kill_blocked=true
  fi
fi

readonly_control_delete_blocked=false
if docker exec "${CONTAINER_NAME}" bash -lc "rm -f /workspace/control/claw-main.txt" >/dev/null 2>&1; then
  readonly_control_delete_blocked=false
else
  if docker exec -u 0 "${CONTAINER_NAME}" bash -lc "test -f /workspace/control/claw-main.txt" >/dev/null 2>&1; then
    readonly_control_delete_blocked=true
  fi
fi

task_workspace_write_succeeded=false
if docker exec "${CONTAINER_NAME}" bash -lc "echo task-ok > /workspace/task/task.txt && test -f /workspace/task/task.txt" >/dev/null 2>&1; then
  task_workspace_write_succeeded=true
fi

python3 - <<'PY' "${cross_user_kill_blocked}" "${readonly_control_delete_blocked}" "${task_workspace_write_succeeded}"
import json
import sys

result = {
    "crossUserKillBlocked": sys.argv[1] == "true",
    "readonlyControlDeleteBlocked": sys.argv[2] == "true",
    "taskWorkspaceWriteSucceeded": sys.argv[3] == "true",
}
print(json.dumps(result, ensure_ascii=False))
if not result["crossUserKillBlocked"]:
    raise SystemExit("expected cross-user kill to be blocked")
if not result["readonlyControlDeleteBlocked"]:
    raise SystemExit("expected readonly control delete to be blocked")
if not result["taskWorkspaceWriteSucceeded"]:
    raise SystemExit("expected task workspace write to remain available")
print("same-container control-plane mitigation verified")
PY
