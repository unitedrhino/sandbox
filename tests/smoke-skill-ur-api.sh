#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
source "${DIR}/lib/docker-network.sh"
PORT="${PORT:-18086}"
MOCK_PORT="${MOCK_PORT:-19091}"
WORKDIR="$(mktemp -d /tmp/claw-skill-ur-api-XXXXXX)"
CONTAINER_NAME="claw-skill-ur-api-smoke"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  if [[ -f /tmp/claw-skill-ur-api-mock.pid ]]; then
    kill "$(cat /tmp/claw-skill-ur-api-mock.pid)" >/dev/null 2>&1 || true
    wait "$(cat /tmp/claw-skill-ur-api-mock.pid)" >/dev/null 2>&1 || true
    rm -f /tmp/claw-skill-ur-api-mock.pid
  fi
  rm -rf "${WORKDIR}"
}
trap cleanup EXIT

cd "${ROOT}"
go build -o claw .
chmod 0777 "${WORKDIR}"
python3 tests/mock_ur_api.py "${MOCK_PORT}" >/tmp/claw-skill-ur-api-mock.log 2>&1 &
echo $! >/tmp/claw-skill-ur-api-mock.pid

docker build -f Dockerfile.local -t claw:dev ..
ensure_claw_docker_network "${NETWORK_NAME}"
docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  --privileged \
  --add-host host.docker.internal:host-gateway \
  -p "${PORT}:8080" \
  -e CLAW_RUNTIME_ID=rt-skill \
  -e CLAW_TENANT_CODE=t1 \
  -e CLAW_CLONE_ID=c1 \
  -e CLAW_CLONE_KEY=clone-a \
  -e CLAW_WORKSPACE=/workspace \
  -e CLAW_CONTROL_DIR=/runtime/control \
  -e CLAW_RUNNER_UID=10001 \
  -e CLAW_RUNNER_GID=10001 \
  -e CLAW_ENABLE_MOUNT_SANDBOX=true \
  -e UR_BASE_URL="http://host.docker.internal:${MOCK_PORT}" \
  -e UR_APP_ID=77 \
  -e UR_TENANT_CODE=default \
  -e UR_ACCOUNT=administrator \
  -e UR_PASSWORD=iThings666 \
  -v "${WORKDIR}:/workspace" \
  claw:dev >/dev/null

for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

echo "=== start ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/start"
echo

echo "=== ur-api check ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","ur-api check"],"timeoutSeconds":10}'
echo

echo "=== claw-skill ur-api get-self ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","claw-skill ur-api get-self"],"timeoutSeconds":10}'
echo
