#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-$(cd "${DIR}/../../../.." && pwd)}"
PKG="${ROOT}/backend/share/proxy"

if [[ -n "${HOST_GOROOT:-}" && -x "${HOST_GOROOT}/bin/go" ]]; then
  export GOROOT="${HOST_GOROOT}"
  export PATH="${HOST_GOROOT}/bin:${PATH}"
  export GOTOOLCHAIN=local
fi
if [[ -n "${HOST_GOMODCACHE:-}" && -d "${HOST_GOMODCACHE}" ]]; then
  export GOMODCACHE="${HOST_GOMODCACHE}"
fi
export GOCACHE="${GOCACHE:-/tmp/go-build}"
mkdir -p "${GOCACHE}"

USE_SUDO=true
if [[ "$(id -u)" -eq 0 ]]; then
  USE_SUDO=false
elif ! sudo -n true >/dev/null 2>&1; then
  echo "sudo without password is required" >&2
  exit 1
fi

cd "${PKG}"

TMP_GO="$(mktemp /tmp/claw-sandbox-net-XXXXXX.go)"
TMP_BIN="$(mktemp /tmp/claw-sandbox-net-bin-XXXXXX)"
cleanup() {
  rm -f "${TMP_GO}"
  rm -f "${TMP_BIN}"
}
trap cleanup EXIT

cat > "${TMP_GO}" <<'GO'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"gitee.com/unitedrhino/share/proxy"
)

func waitForNewNetns(pid int, timeout time.Duration) error {
	selfNS, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		targetNS, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", pid))
		if err == nil && targetNS != selfNS {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("pid %d did not enter a new network namespace within %v", pid, timeout)
}

func main() {
	type result struct {
		HasCgroupOrRootSupport bool   `json:"hasRootSupport"`
		SetupOK                bool   `json:"setupOk"`
		StartOK                bool   `json:"startOk"`
		ProxyURL               string `json:"proxyUrl"`
		CleanupOK              bool   `json:"cleanupOk"`
		Error                  string `json:"error,omitempty"`
	}

	res := result{HasCgroupOrRootSupport: os.Geteuid() == 0}

	cmd := exec.Command("unshare", "-n", "sleep", "5")
	if err := cmd.Start(); err != nil {
		res.Error = "start_unshare: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		return
	}
	defer cmd.Process.Kill()
	if err := waitForNewNetns(cmd.Process.Pid, 2*time.Second); err != nil {
		res.Error = "wait_netns: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		return
	}

	p := proxy.NewNetworkProxy(proxy.DefaultNetworkConfig())
	if err := p.Setup(cmd.Process.Pid); err != nil {
		res.Error = "setup: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		return
	}
	res.SetupOK = true
	res.ProxyURL = p.GetProxyURL()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = p.Start(ctx)
	}()
	time.Sleep(500 * time.Millisecond)
	res.StartOK = true

	if err := p.Cleanup(); err == nil {
		res.CleanupOK = true
	} else {
		res.Error = "cleanup: " + err.Error()
	}

	out, _ := json.Marshal(res)
	fmt.Println(string(out))
}
GO

go build -o "${TMP_BIN}" "${TMP_GO}"

set +e
if [[ "${USE_SUDO}" == "true" ]]; then
  OUTPUT="$(sudo -n timeout 10s "${TMP_BIN}" 2>&1)"
  STATUS=$?
else
  OUTPUT="$(timeout 10s "${TMP_BIN}" 2>&1)"
  STATUS=$?
fi
set -e

if [[ ${STATUS} -eq 124 ]]; then
  printf '%s\n' '{"hasRootSupport":true,"setupOk":false,"startOk":false,"cleanupOk":false,"error":"timeout"}'
  exit 0
fi

if [[ ${STATUS} -ne 0 ]]; then
  python3 - <<'PY' "${OUTPUT}"
import json
import sys
print(json.dumps({
    "hasRootSupport": True,
    "setupOk": False,
    "startOk": False,
    "cleanupOk": False,
    "error": sys.argv[1][:500]
}, ensure_ascii=False))
PY
  exit 0
fi

printf '%s\n' "${OUTPUT}"
