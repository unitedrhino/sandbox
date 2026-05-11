#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${DIR}/.." && pwd)"
source "${DIR}/lib/docker-network.sh"
PORT="${PORT:-18092}"
WORKDIR="$(mktemp -d /tmp/claw-skill-shared-XXXXXX)"
SHAREDDIR="$(mktemp -d /tmp/claw-shared-skills-XXXXXX)"
CONTAINER_NAME="claw-skill-shared-smoke"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  rm -rf "${WORKDIR}" "${SHAREDDIR}"
}
trap cleanup EXIT

mkdir -p "${SHAREDDIR}/team-skill/versions/v1/scripts" "${SHAREDDIR}/team-skill/versions/v2/scripts"
cat >"${SHAREDDIR}/team-skill/current" <<'EOF'
v1
EOF
cat >"${SHAREDDIR}/team-skill/versions/v1/SKILL.md" <<'EOF'
# team-skill
EOF
cat >"${SHAREDDIR}/team-skill/versions/v1/scripts/run.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'shared-skill-v1'
EOF
cat >"${SHAREDDIR}/team-skill/versions/v2/SKILL.md" <<'EOF'
# team-skill
EOF
cat >"${SHAREDDIR}/team-skill/versions/v2/scripts/run.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'shared-skill-v2'
EOF
chmod +x "${SHAREDDIR}/team-skill/versions/v1/scripts/run.sh" "${SHAREDDIR}/team-skill/versions/v2/scripts/run.sh"
chmod 0755 "${SHAREDDIR}" "${SHAREDDIR}/team-skill" "${SHAREDDIR}/team-skill/versions" "${SHAREDDIR}/team-skill/versions/v1" "${SHAREDDIR}/team-skill/versions/v1/scripts" "${SHAREDDIR}/team-skill/versions/v2" "${SHAREDDIR}/team-skill/versions/v2/scripts"
chmod 0644 "${SHAREDDIR}/team-skill/current" "${SHAREDDIR}/team-skill/versions/v1/SKILL.md" "${SHAREDDIR}/team-skill/versions/v2/SKILL.md"
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
  -e CLAW_RUNTIME_ID=rt-shared \
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
  -v "${SHAREDDIR}:/opt/skills/shared:ro" \
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

echo "=== skills ==="
curl -s "http://127.0.0.1:${PORT}/runtime/skills"
echo

echo "=== shared skill detail ==="
curl -s "http://127.0.0.1:${PORT}/runtime/skills/team-skill"
echo

echo "=== shared skill run ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","claw-skill team-skill run"],"timeoutSeconds":10}'
echo

echo "=== shared skill activate v2 ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/skills/team-skill/activate" \
  -H 'Content-Type: application/json' \
  -d '{"version":"v2"}'
echo

echo "=== shared skill detail after activate ==="
curl -s "http://127.0.0.1:${PORT}/runtime/skills/team-skill"
echo

echo "=== shared skill run after activate ==="
curl -s -X POST "http://127.0.0.1:${PORT}/runtime/exec" \
  -H 'Content-Type: application/json' \
  -d '{"command":["/bin/sh","-lc","claw-skill team-skill run"],"timeoutSeconds":10}'
echo
