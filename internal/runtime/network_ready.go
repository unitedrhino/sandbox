package runtime

import (
	"context"
	"fmt"
	"net"
	"time"
)

func waitForTCPReady(ctx context.Context, addr string) error {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()

	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("tcp listener %s not ready before timeout", addr)
		case <-ticker.C:
		}
	}
}
