#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
source "${DIR}/lib/docker-network.sh"
PORT="${PORT:-18096}"
WORKDIR="$(mktemp -d /tmp/claw-skill-direct-path-XXXXXX)"
MAPPEDDIR="$(mktemp -d /tmp/claw-direct-path-mapped-XXXXXX)"
CONTAINER_NAME="claw-skill-direct-path-smoke"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  rm -rf "${WORKDIR}" "${MAPPEDDIR}"
}
trap cleanup EXIT

mkdir -p "${MAPPEDDIR}/demo-skill/versions/v1/scripts"
cat >"${MAPPEDDIR}/demo-skill/current" <<'EOF'
v1
EOF
cat >"${MAPPEDDIR}/demo-skill/versions/v1/SKILL.md" <<'EOF'
# demo-skill
EOF
cat >"${MAPPEDDIR}/demo-skill/versions/v1/scripts/run.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'should-not-run'
EOF
chmod +x "${MAPPEDDIR}/demo-skill/versions/v1/scripts/run.sh"
chmod 0755 "${MAPPEDDIR}" "${MAPPEDDIR}/demo-skill" "${MAPPEDDIR}/demo-skill/versions" "${MAPPEDDIR}/demo-skill/versions/v1" "${MAPPEDDIR}/demo-skill/versions/v1/scripts"
chmod 0644 "${MAPPEDDIR}/demo-skill/current" "${MAPPEDDIR}/demo-skill/versions/v1/SKILL.md"
chmod 0777 "${WORKDIR}"

cd "${ROOT}"
go build -o claw .
docker build -f Dockerfile.local -t claw:dev ..
ensure_claw_docker_network "${NETWORK_NAME}"
docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  --privileged \
  -p "${PORT}:8080" \
  -e CLAW_RUNTIME_ID=rt-direct-path \
  -e CLAW_TENANT_CODE=t1 \
  -e CLAW_CLONE_ID=c1 \
  -e CLAW_CLONE_KEY=clone-a \
  -e CLAW_WORKSPACE=/workspace \
  -e CLAW_CONTROL_DIR=/runtime/control \
  -e CLAW_RUNNER_UID=10001 \
  -e CLAW_RUNNER_GID=10001 \
  -e CLAW_ENABLE_SANDBOX_NET=true \
  -e CLAW_ENABLE_MOUNT_SANDBOX=true \
  -v "${WORKDIR}:/workspace" \
  -v "${MAPPEDDIR}:/opt/skills/mapped:ro" \
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

echo "=== direct path execution (should fail) ==="
set +e
HTTP_BODY="$(mktemp /tmp/claw-direct-path-body-XXXXXX)"
HTTP_CODE="$(curl -s -o "${HTTP_BODY}" -w "%{http_code}" -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","bash /opt/skills/mapped/demo-skill/versions/v1/scripts/run.sh"],"timeoutSeconds":10}')"
set -e
cat "${HTTP_BODY}"
echo
echo "http_code=${HTTP_CODE}"
rm -f "${HTTP_BODY}"
