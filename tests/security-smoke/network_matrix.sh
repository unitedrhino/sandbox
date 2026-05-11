#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${DIR}/../lib/docker-network.sh"
IMAGE_TAG="${IMAGE_TAG:-claw-runtime:node24-python}"
CONTAINER_NAME="${CONTAINER_NAME:-claw-network-matrix}"
NETWORK_NAME="${NETWORK_NAME:-$(claw_default_network_name "${CONTAINER_NAME}")}"
JSON_MODE="${1:-}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  cleanup_claw_docker_network "${NETWORK_NAME}"
}
trap cleanup EXIT

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
  "${IMAGE_TAG}" >/dev/null

NETWORK_INFO="$(
docker inspect "${CONTAINER_NAME}" --format '{{json .NetworkSettings.Networks}}'
)"

RESULTS="$(
docker exec "${CONTAINER_NAME}" python3 -c "
import json,socket,struct
gw=None
f=open('/proc/net/route'); next(f)
for line in f:
    fields=line.split()
    if fields[1]=='00000000' and (int(fields[3],16)&2):
        gw=socket.inet_ntoa(struct.pack('<L', int(fields[2],16)))
        break
def check(host, port, timeout=1.0):
    s=socket.socket(); s.settimeout(timeout)
    try:
        s.connect((host, port)); return True
    except Exception:
        return False
    finally:
        s.close()
targets = {
    'host_gateway': gw,
    'host_published_postgres_5432': check(gw, 5432) if gw else False,
    'host_published_redis_6379': check(gw, 6379) if gw else False,
    'host_published_lightrag_9621': check(gw, 9621) if gw else False,
    'host_unpublished_mysql_3306': check(gw, 3306) if gw else False,
    'host_published_minio_9000': check(gw, 9000) if gw else False,
    'private_10_net_10_0_0_1_80': check('10.0.0.1', 80),
    'private_172_net_172_16_0_1_80': check('172.16.0.1', 80),
    'private_192_net_192_168_1_1_80': check('192.168.1.1', 80),
    'link_local_169_254_169_254_80': check('169.254.169.254', 80),
    'localhost_127_0_0_1_80': check('127.0.0.1', 80),
    'public_dns_8_8_8_8_53': check('8.8.8.8', 53),
    'public_https_1_1_1_1_443': check('1.1.1.1', 443),
}
print(json.dumps(targets, ensure_ascii=False))
"
)"

if [[ "${JSON_MODE}" == "--json" ]]; then
  python3 - <<'PY' "${NETWORK_INFO}" "${RESULTS}"
import json
import sys

networks = json.loads(sys.argv[1])
targets = json.loads(sys.argv[2])
targets['attached_networks'] = sorted(networks.keys())
targets['default_bridge_absent'] = 'bridge' not in networks
targets['claw_network_present'] = len(networks) == 1
print(json.dumps(targets, ensure_ascii=False))
PY
else
  python3 - <<'PY' "${NETWORK_INFO}" "${RESULTS}"
import json
import sys

networks = json.loads(sys.argv[1])
data = json.loads(sys.argv[2])
data["attached_networks"] = sorted(networks.keys())
data["default_bridge_absent"] = "bridge" not in networks
data["claw_network_present"] = len(networks) == 1
print("=== claw network matrix ===")
for key, value in data.items():
    print(f"{key}={str(value).lower()}")
PY
fi
