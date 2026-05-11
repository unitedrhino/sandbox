//go:build linux

package runtime

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

func TestOSExecutor_SandboxNetworkBlocksPrivateDirectConnect(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	ln, err := net.Listen("tcp", "0.0.0.0:15432")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	exec := NewOSExecutor(config.ExecOptions{
		RunnerUID:        uint32(os.Getuid()),
		RunnerGID:        uint32(os.Getgid()),
		SandboxNetEnable: true,
		SandboxProxyPort: 1080,
		BlockedCIDRs:     []string{"10.233.1.0/24"},
		CPUQuota:         50,
		MemoryLimitMB:    128,
		MaxProcesses:     32,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := exec.Execute(ctx, ExecRequest{
		Command:   []string{"/bin/sh", "-lc", "python3 - <<'PY'\nimport socket\ns=socket.socket(); s.settimeout(1)\ns.connect(('10.233.1.2',15432))\nprint('connected')\nPY"},
		Workspace: t.TempDir(),
		Timeout:   2 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected error, got result %+v", result)
	}
}
