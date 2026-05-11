package runtime

import (
	"bytes"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestCommandStartGateBlocksUntilRelease(t *testing.T) {
	gate, err := newCommandStartGate()
	if err != nil {
		t.Fatalf("newCommandStartGate() error = %v", err)
	}
	defer gate.Close()

	command := gate.Wrap([]string{"/bin/sh", "-lc", "echo ready"})
	cmd := exec.Command(command[0], command[1:]...)
	gate.Attach(cmd)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	defer func() {
		_ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	}()

	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process exited before gate release: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should stay empty before release, got %q", stdout.String())
	}

	if err := gate.Release(); err != nil {
		t.Fatalf("gate.Release() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cmd.Wait() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not finish after gate release")
	}

	if got := stdout.String(); got != "ready\n" {
		t.Fatalf("stdout = %q", got)
	}
}
