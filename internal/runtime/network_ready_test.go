package runtime

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestWaitForTCPReadyWaitsForLateListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("ln.Close() error = %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		lateLn, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		defer lateLn.Close()
		conn, err := lateLn.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := waitForTCPReady(ctx, addr); err != nil {
		t.Fatalf("waitForTCPReady() error = %v", err)
	}
}
