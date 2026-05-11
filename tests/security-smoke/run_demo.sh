#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${DIR}/../lib/docker-network.sh"
IMAGE_TAG="${IMAGE_TAG:-claw-runtime:node24-python}"
CONTAINER_NAME="${CONTAINER_NAME:-claw-security-demo}"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"
JSON_MODE="${1:-}"

WORK_ROOT="$(mktemp -d /tmp/claw-security-demo-XXXXXX)"
WORKSPACE_DIR="${WORK_ROOT}/workspace"
HOST_SECRET_DIR="${WORK_ROOT}/host-secret"
READONLY_DIR="${WORK_ROOT}/readonly"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
  chmod -R u+w "${WORK_ROOT}" >/dev/null 2>&1 || true
  rm -rf "${WORK_ROOT}"
}
trap cleanup EXIT

mkdir -p "${WORKSPACE_DIR}" "${HOST_SECRET_DIR}" "${READONLY_DIR}"
printf 'allowed-from-workspace\n' > "${WORKSPACE_DIR}/allowed.txt"
printf 'secret-outside-workspace\n' > "${HOST_SECRET_DIR}/secret.txt"
printf 'readonly-seed\n' > "${READONLY_DIR}/seed.txt"
chmod 0777 "${WORKSPACE_DIR}"
chmod 0555 "${READONLY_DIR}"

cat > "${WORKSPACE_DIR}/try-read-outside.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
cat /host-secret/secret.txt
SH
chmod +x "${WORKSPACE_DIR}/try-read-outside.sh"

cat > "${WORKSPACE_DIR}/try-docker.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
docker ps
SH
chmod +x "${WORKSPACE_DIR}/try-docker.sh"

if ! docker image inspect "${IMAGE_TAG}" >/dev/null 2>&1; then
  docker build -t "${IMAGE_TAG}" "${DIR}" >/dev/null
fi

ensure_claw_docker_network "${NETWORK_NAME}"
docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
docker run -d \
  --name "${CONTAINER_NAME}" \
  --network "${NETWORK_NAME}" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --mount "type=bind,src=${WORKSPACE_DIR},dst=/workspace" \
  --mount "type=bind,src=${READONLY_DIR},dst=/readonly,readonly" \
  "${IMAGE_TAG}" >/dev/null

run_in_container() {
  docker exec "${CONTAINER_NAME}" sh -lc "$1"
}

check_ok() {
  if run_in_container "$1" >/dev/null 2>&1; then
    echo true
  else
    echo false
  fi
}

check_fail() {
  if run_in_container "$1" >/dev/null 2>&1; then
    echo false
  else
    echo true
  fi
}

workspace_read_allowed="$(check_ok "cat /workspace/allowed.txt")"
host_secret_blocked="$(check_fail "cat /host-secret/secret.txt")"
script_wrapped_host_secret_blocked="$(check_fail "/workspace/try-read-outside.sh")"
readonly_mount_write_blocked="$(check_fail "echo blocked > /readonly/blocked.txt")"
docker_sock_absent="$(check_fail "test -S /var/run/docker.sock")"
docker_command_blocked="$(check_fail "docker ps")"
script_wrapped_docker_blocked="$(check_fail "/workspace/try-docker.sh")"
host_published_internal_service_reachable="$(check_ok "python3 -c \"import socket,struct; gw=None; f=open('/proc/net/route'); next(f); [globals().__setitem__('gw', socket.inet_ntoa(struct.pack('<L', int(line.split()[2],16)))) or None for line in f if line.split()[1]=='00000000' and (int(line.split()[3],16)&2)]; s=socket.socket(); s.settimeout(1); s.connect((gw,5432)); s.close()\"")"
container_rootfs_readable="$(check_ok "cat /etc/passwd")"
container_tmp_writable="$(check_ok "echo test > /tmp/claw-demo.txt && test -f /tmp/claw-demo.txt")"
tmp_quota_enforced="$(check_fail "python3 - <<'PY'\nwith open('/tmp/big.bin','wb') as f:\n    f.write(b'0'*(70*1024*1024))\nPY")"
workspace_bind_mount_quota_not_enforced="$(check_ok "python3 -c \"f=open('/workspace/big.bin','wb'); f.write(b'0'*(70*1024*1024)); f.close()\"")"

python3 - <<'PY' \
  "${workspace_read_allowed}" \
  "${host_secret_blocked}" \
  "${script_wrapped_host_secret_blocked}" \
  "${readonly_mount_write_blocked}" \
  "${docker_sock_absent}" \
  "${docker_command_blocked}" \
  "${script_wrapped_docker_blocked}" \
  "${host_published_internal_service_reachable}" \
  "${container_rootfs_readable}" \
  "${container_tmp_writable}" \
  "${tmp_quota_enforced}" \
  "${workspace_bind_mount_quota_not_enforced}" \
  "${JSON_MODE}"
import json
import sys

keys = [
    "workspace_read_allowed",
    "host_secret_blocked",
    "script_wrapped_host_secret_blocked",
    "readonly_mount_write_blocked",
    "docker_sock_absent",
    "docker_command_blocked",
    "script_wrapped_docker_blocked",
    "host_published_internal_service_reachable",
    "container_rootfs_readable",
    "container_tmp_writable",
    "tmp_quota_enforced",
    "workspace_bind_mount_quota_not_enforced",
]
values = {k: (v == "true") for k, v in zip(keys, sys.argv[1:13])}
json_mode = sys.argv[13] == "--json"

if json_mode:
    print(json.dumps(values, ensure_ascii=False))
else:
    print("=== claw security smoke demo ===")
    for k in keys:
        print(f"{k}={str(values[k]).lower()}")
    print()
    print("Interpretation:")
    print("- true on *_blocked / *_absent means the defense worked")
    print("- true on host_published_internal_service_reachable means the container can still reach host-exposed internal ports")
    print("- true on container_rootfs_readable / container_tmp_writable means current image still exposes container internal rootfs and /tmp")
    print("- true on workspace_bind_mount_quota_not_enforced means current bind mount is not covered by the tmpfs quota")
PY
