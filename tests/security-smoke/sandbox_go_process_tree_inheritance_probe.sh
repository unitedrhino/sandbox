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

TMP_GO="$(mktemp /tmp/claw-sandbox-tree-XXXXXX.go)"
TMP_BIN="$(mktemp /tmp/claw-sandbox-tree-bin-XXXXXX)"
cleanup() {
  rm -f "${TMP_GO}" "${TMP_BIN}"
}
trap cleanup EXIT

cat > "${TMP_GO}" <<'GO'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

func runPythonInNetns(pid int, code string, args ...string) (string, error) {
	cmdArgs := []string{"-t", fmt.Sprintf("%d", pid), "-n", "python3", "-c", code}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("nsenter", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func main() {
	type procResult struct {
		Name            string `json:"name"`
		Netns           string `json:"netns"`
		DirectConnectOK bool   `json:"directConnectOk"`
		ProxyPortOK     bool   `json:"proxyPortOk"`
	}
	type result struct {
		HasRootSupport bool         `json:"hasRootSupport"`
		SetupOK        bool         `json:"setupOk"`
		StartOK        bool         `json:"startOk"`
		ProcessResults []procResult `json:"processResults"`
		Error          string       `json:"error,omitempty"`
	}

	res := result{HasRootSupport: os.Geteuid() == 0}

	child := exec.Command("unshare", "-n", "sleep", "20")
	if err := child.Start(); err != nil {
		res.Error = "start_unshare: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		os.Exit(1)
	}
	defer child.Process.Kill()

	if err := waitForNewNetns(child.Process.Pid, 2*time.Second); err != nil {
		res.Error = "wait_netns: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		os.Exit(1)
	}

	p := proxy.NewNetworkProxy(proxy.DefaultNetworkConfig())
	if err := p.Setup(child.Process.Pid); err != nil {
		res.Error = "setup: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		os.Exit(1)
	}
	res.SetupOK = true
	defer p.Cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Start(ctx) }()
	time.Sleep(500 * time.Millisecond)
	res.StartOK = true

	ln, err := net.Listen("tcp", net.JoinHostPort(p.GetBridgeIP(), "15432"))
	if err != nil {
		res.Error = "listen: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		os.Exit(1)
	}
	defer ln.Close()
	go func() {
		for i := 0; i < 4; i++ {
			conn, err := ln.Accept()
			if err == nil {
				_ = conn.Close()
			}
		}
	}()

	pythonCode := `
import json
import os
import socket
import subprocess
import sys

host = sys.argv[1]
probe_port = int(sys.argv[2])
proxy_port = int(sys.argv[3])

def can_connect(port):
    s = socket.socket()
    s.settimeout(1)
    try:
        s.connect((host, port))
        return True
    except Exception:
        return False
    finally:
        s.close()

def collect(name):
    return {
        "name": name,
        "netns": os.readlink("/proc/self/ns/net"),
        "directConnectOk": can_connect(probe_port),
        "proxyPortOk": can_connect(proxy_port),
    }

child_code = """
import json, os, socket, subprocess, sys
host=sys.argv[1]; probe_port=int(sys.argv[2]); proxy_port=int(sys.argv[3])
def can_connect(port):
    s=socket.socket(); s.settimeout(1)
    try:
        s.connect((host, port)); return True
    except Exception:
        return False
    finally:
        s.close()
data=[{
    'name':'child',
    'netns': os.readlink('/proc/self/ns/net'),
    'directConnectOk': can_connect(probe_port),
    'proxyPortOk': can_connect(proxy_port),
}]
grand_code = '''import json, os, socket, sys
host=sys.argv[1]; probe_port=int(sys.argv[2]); proxy_port=int(sys.argv[3])
def can_connect(port):
    s=socket.socket(); s.settimeout(1)
    try:
        s.connect((host, port)); return True
    except Exception:
        return False
    finally:
        s.close()
print(json.dumps({
  "name":"grandchild",
  "netns": os.readlink("/proc/self/ns/net"),
  "directConnectOk": can_connect(probe_port),
  "proxyPortOk": can_connect(proxy_port),
}))'''
grand = subprocess.check_output([sys.executable, "-c", grand_code, host, str(probe_port), str(proxy_port)], text=True)
data.append(json.loads(grand))
print(json.dumps(data))
"""

results = [collect("parent")]
child_out = subprocess.check_output([sys.executable, "-c", child_code, host, str(probe_port), str(proxy_port)], text=True)
results.extend(json.loads(child_out))
print(json.dumps(results))
`

	out, err := runPythonInNetns(child.Process.Pid, pythonCode, p.GetBridgeIP(), "15432", "1080")
	if err != nil {
		res.Error = "process_tree_probe: " + err.Error()
		outb, _ := json.Marshal(res)
		fmt.Println(string(outb))
		os.Exit(1)
	}

	if err := json.Unmarshal([]byte(out), &res.ProcessResults); err != nil {
		res.Error = "parse_process_results: " + err.Error()
		outb, _ := json.Marshal(res)
		fmt.Println(string(outb))
		os.Exit(1)
	}

	outb, _ := json.Marshal(res)
	fmt.Println(string(outb))
}
GO

go build -o "${TMP_BIN}" "${TMP_GO}"

set +e
if [[ "${USE_SUDO}" == "true" ]]; then
  OUTPUT="$(sudo -n timeout 20s "${TMP_BIN}" 2>&1)"
  STATUS=$?
else
  OUTPUT="$(timeout 20s "${TMP_BIN}" 2>&1)"
  STATUS=$?
fi
set -e

if [[ ${STATUS} -eq 124 ]]; then
  printf '%s\n' '{"hasRootSupport":true,"setupOk":false,"startOk":false,"error":"timeout"}'
  exit 1
fi

printf '%s\n' "${OUTPUT}"

python3 - <<'PY' "${OUTPUT}"
import json
import sys

text = sys.argv[1].strip().splitlines()[-1]
data = json.loads(text)

if not data.get("setupOk"):
    raise SystemExit("setup failed")
if not data.get("startOk"):
    raise SystemExit("proxy start failed")

results = data.get("processResults", [])
if len(results) != 3:
    raise SystemExit(f"expected 3 process results, got {results}")

netns = {item["netns"] for item in results}
if len(netns) != 1:
    raise SystemExit(f"expected same netns for parent/child/grandchild, got {results}")

for item in results:
    if item.get("directConnectOk"):
        raise SystemExit(f"direct connect should be blocked for {item}")
    if not item.get("proxyPortOk"):
        raise SystemExit(f"proxy port should remain reachable for {item}")

print("process-tree inheritance probe verified")
PY
