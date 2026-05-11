#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
BACKEND_ROOT="$(cd "${ROOT}/.." && pwd)"
source "${DIR}/lib/docker-network.sh"
PORT="${PORT:-18084}"
WORKDIR="$(mktemp -d /tmp/claw-docker-smoke-XXXXXX)"
TARGETDIR="$(mktemp -d /tmp/claw-docker-target-XXXXXX)"
CONTAINER_NAME="claw-docker-smoke"
TARGET_CONTAINER_NAME="claw-docker-proxy-target"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"
TENANT_CODE="t1"
CLONE_ID="c1"
CLONE_KEY="clone-a"
EFFECTIVE_WORKSPACE="${WORKDIR}/${TENANT_CODE}/${CLONE_KEY}/${CLONE_ID}/work"

cleanup() {
  docker exec -u 0 "${CONTAINER_NAME}" bash -lc "chmod -R 0777 /workspace >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  docker rm -f "${TARGET_CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  rm -rf "${WORKDIR}"
  rm -rf "${TARGETDIR}"
}
trap cleanup EXIT

chmod 0777 "${WORKDIR}"
chmod 0777 "${TARGETDIR}"
printf 'proxy-ok\n' > "${TARGETDIR}/ok.txt"

cd "${ROOT}"
go build -o claw .
docker build -f Dockerfile.local -t claw:dev ..
ensure_claw_docker_network "${NETWORK_NAME}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
docker rm -f "${TARGET_CONTAINER_NAME}" >/dev/null 2>&1 || true
docker run -d \
  --name "${TARGET_CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  -v "${TARGETDIR}:/www:ro" \
  busybox:1.36 \
  sh -c 'httpd -f -p 8081 -h /www' >/dev/null

TARGET_IP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${TARGET_CONTAINER_NAME}")"

docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  --privileged \
  -p "${PORT}:8080" \
  -e CLAW_RUNTIME_ID=rt-docker \
  -e CLAW_TENANT_CODE="${TENANT_CODE}" \
  -e CLAW_CLONE_ID="${CLONE_ID}" \
  -e CLAW_CLONE_KEY="${CLONE_KEY}" \
  -e CLAW_WORKSPACE=/workspace \
  -e CLAW_CONTROL_DIR=/runtime/control \
  -e CLAW_RUNNER_UID=10001 \
  -e CLAW_RUNNER_GID=10001 \
  -e CLAW_ENABLE_SANDBOX_NET=true \
  -e CLAW_ENABLE_MOUNT_SANDBOX=true \
  -e CLAW_SANDBOX_ALLOWED_CIDRS="${TARGET_IP}/32" \
  -e CLAW_SANDBOX_ALLOWED_PORTS=8081 \
  -e OPENAI_API_KEY=sk-docker-test \
  -e OPENAI_MODEL=gpt-docker-test \
  -v "${WORKDIR}:/workspace" \
  claw:dev >/dev/null

for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

echo "=== healthz ==="
curl -s "http://127.0.0.1:${PORT}/healthz"
echo

echo "=== start ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start"
echo

echo "=== runtime status ==="
curl -s "http://127.0.0.1:${PORT}/runtime/status"
echo

echo "=== curl available in image ==="
docker exec "${CONTAINER_NAME}" bash -lc "command -v curl"
echo

echo "=== control dir perms ==="
docker exec "${CONTAINER_NAME}" bash -lc "stat -c '%U %G %a %n' /runtime/control /runtime/control/main.pid"
echo

echo "=== task workspace write ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","printf task-ok > /workspace/task.txt && cat /workspace/task.txt"],"timeoutSeconds":3}'
echo

echo "=== model env injected ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","printf \"$OPENAI_API_KEY|$OPENAI_MODEL\""],"timeoutSeconds":3}'
echo

echo "=== curl runnable in task ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","curl --version | head -n 1"],"timeoutSeconds":3}'
echo

echo "=== curl proxy allowlist success ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d "{\"command\":[\"/bin/sh\",\"-lc\",\"curl -sS --connect-timeout 3 --max-time 4 http://${TARGET_IP}:8081/ok.txt\"],\"timeoutSeconds\":10}"
echo

echo "=== curl direct connect blocked without proxy env ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d "{\"command\":[\"/bin/sh\",\"-lc\",\"env -u ALL_PROXY -u all_proxy -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy curl -sS --connect-timeout 3 --max-time 4 http://${TARGET_IP}:8081/ok.txt\"],\"timeoutSeconds\":10}"
echo

echo "=== control dir hidden in mount sandbox ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","ls -ld /runtime/control"],"timeoutSeconds":3}'
echo

echo "=== task file owner ==="
docker exec "${CONTAINER_NAME}" bash -lc "stat -c '%u %g %a %n' /workspace/${TENANT_CODE}/${CLONE_KEY}/${CLONE_ID}/work/task.txt"
echo

echo "=== host clone workspace path ==="
if [[ ! -f "${EFFECTIVE_WORKSPACE}/task.txt" ]]; then
  echo "expected task file under ${EFFECTIVE_WORKSPACE}/task.txt" >&2
  exit 1
fi
stat -c '%u %g %a %n' "${EFFECTIVE_WORKSPACE}/task.txt"
echo

echo "=== control file delete blocked ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","rm /runtime/control/main.pid"],"timeoutSeconds":3}'
echo

echo "=== control process kill blocked ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","cat /runtime/control/main.pid && kill -KILL $(cat /runtime/control/main.pid)"],"timeoutSeconds":3}'
echo

echo "=== healthz after attacks ==="
curl -s "http://127.0.0.1:${PORT}/healthz"
echo
