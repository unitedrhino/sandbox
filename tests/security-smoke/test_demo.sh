#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="${DIR}/run_demo.sh"

if [[ ! -x "${RUNNER}" ]]; then
  echo "run_demo.sh not found or not executable: ${RUNNER}" >&2
  exit 1
fi

JSON_OUTPUT="$("${RUNNER}" --json)"

EXPECTED_HOST_PUBLISHED_INTERNAL_SERVICE_REACHABLE="${EXPECTED_HOST_PUBLISHED_INTERNAL_SERVICE_REACHABLE:-true}"

python3 - <<'PY' "${JSON_OUTPUT}"
import json
import os
import sys

data = json.loads(sys.argv[1])

expected = {
    "workspace_read_allowed": True,
    "host_secret_blocked": True,
    "script_wrapped_host_secret_blocked": True,
    "readonly_mount_write_blocked": True,
    "docker_sock_absent": True,
    "docker_command_blocked": True,
    "script_wrapped_docker_blocked": True,
    "host_published_internal_service_reachable": os.environ.get("EXPECTED_HOST_PUBLISHED_INTERNAL_SERVICE_REACHABLE", "true").lower() == "true",
    "container_rootfs_readable": True,
    "container_tmp_writable": True,
    "tmp_quota_enforced": True,
    "workspace_bind_mount_quota_not_enforced": True,
}

missing = [k for k in expected if k not in data]
if missing:
    raise SystemExit(f"missing keys: {missing}")

wrong = {k: (data[k], v) for k, v in expected.items() if data[k] != v}
if wrong:
    raise SystemExit(f"unexpected values: {wrong}")

print("security demo expectations verified")
PY
