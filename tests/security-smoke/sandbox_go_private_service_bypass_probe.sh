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

TMP_GO="$(mktemp /tmp/claw-sandbox-bypass-XXXXXX.go)"
TMP_BIN="$(mktemp /tmp/claw-sandbox-bypass-bin-XXXXXX)"
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
	type result struct {
		HasRootSupport     bool   `json:"hasRootSupport"`
		SetupOK            bool   `json:"setupOk"`
		StartOK            bool   `json:"startOk"`
		ProbeListenAddr    string `json:"probeListenAddr"`
		DirectConnectOK    bool   `json:"directConnectOk"`
		DirectConnectError string `json:"directConnectError,omitempty"`
		ProxyPortOK        bool   `json:"proxyPortOk"`
		ProxyPortError     string `json:"proxyPortError,omitempty"`
		SocksBlocked       bool   `json:"socksBlocked"`
		SocksReplyCode     int    `json:"socksReplyCode,omitempty"`
		Error              string `json:"error,omitempty"`
	}

	res := result{HasRootSupport: os.Geteuid() == 0}

	child := exec.Command("unshare", "-n", "sleep", "15")
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

	listenAddr := net.JoinHostPort(p.GetBridgeIP(), "15432")
	res.ProbeListenAddr = listenAddr
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		res.Error = "listen: " + err.Error()
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
		os.Exit(1)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	directCode := `
import socket, sys
host = sys.argv[1]
port = int(sys.argv[2])
s = socket.socket()
s.settimeout(2)
try:
    s.connect((host, port))
    print("direct-ok")
except Exception as e:
    print("direct-err:" + str(e))
    raise
finally:
    s.close()
`
	if out, err := runPythonInNetns(child.Process.Pid, directCode, p.GetBridgeIP(), "15432"); err == nil {
		_ = out
		res.DirectConnectOK = true
	} else {
		res.DirectConnectError = err.Error()
	}

	if out, err := runPythonInNetns(child.Process.Pid, directCode, p.GetBridgeIP(), "1080"); err == nil {
		_ = out
		res.ProxyPortOK = true
	} else {
		res.ProxyPortError = err.Error()
	}

	socksCode := `
import socket, struct, sys
proxy_host = sys.argv[1]
proxy_port = int(sys.argv[2])
target_host = sys.argv[3]
target_port = int(sys.argv[4])
s = socket.socket()
s.settimeout(3)
s.connect((proxy_host, proxy_port))
s.sendall(b"\x05\x01\x00")
resp = s.recv(2)
if len(resp) != 2 or resp[1] != 0:
    raise SystemExit("bad-handshake:" + repr(resp))
req = b"\x05\x01\x00\x01" + socket.inet_aton(target_host) + struct.pack("!H", target_port)
s.sendall(req)
resp = s.recv(10)
if len(resp) < 2:
    raise SystemExit("short-reply")
print(resp[1])
s.close()
`
	out, err := runPythonInNetns(child.Process.Pid, socksCode, p.GetBridgeIP(), "1080", p.GetBridgeIP(), "15432")
	if err != nil {
		res.Error = "socks_probe: " + err.Error()
		outb, _ := json.Marshal(res)
		fmt.Println(string(outb))
		os.Exit(1)
	}

	var replyCode int
	if _, err := fmt.Sscanf(out, "%d", &replyCode); err != nil {
		res.Error = "parse_socks_reply: " + err.Error()
		outb, _ := json.Marshal(res)
		fmt.Println(string(outb))
		os.Exit(1)
	}
	res.SocksReplyCode = replyCode
	res.SocksBlocked = replyCode == 0x05

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
  printf '%s\n' '{"hasRootSupport":true,"setupOk":false,"startOk":false,"socksBlocked":false,"error":"timeout"}'
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
if data.get("directConnectOk"):
    raise SystemExit(f"direct private connect should be blocked: {data}")
if not data.get("proxyPortOk"):
    raise SystemExit(f"proxy port should remain reachable: {data}")
if not data.get("socksBlocked"):
    raise SystemExit(f"socks path did not block private target: {data}")

print("private-service bypass probe verified")
PY
