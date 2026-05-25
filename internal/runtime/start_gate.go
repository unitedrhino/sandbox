package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type commandStartGate struct {
	readPipe  *os.File
	writePipe *os.File
	release   sync.Once
	close     sync.Once
}

func newCommandStartGate() (*commandStartGate, error) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create command start gate: %w", err)
	}
	return &commandStartGate{
		readPipe:  readPipe,
		writePipe: writePipe,
	}, nil
}

func (g *commandStartGate) Wrap(command []string) []string {
	// 使用 dd 直接从 fd 3 读取一个字节来阻塞等待父进程发出信号。
	// 不能使用 [ -r /proc/self/fd/3 ] 做检测，因为当 CLONE_NEWNET 与
	// credential drop (SysProcAttr.Credential) 同时使用时，/proc/self/fd/3
	// 的 access() 权限检查会错误地返回不可读（即使 fd 实际可用）。
	// 直接尝试读取，dd 失败时通过 || true 兜底继续执行。
	args := []string{
		"/bin/sh",
		"-c",
		`dd bs=1 count=1 <&3 >/dev/null 2>&1 || true; exec "$@"`,
		"sandbox-net-bootstrap",
	}
	args = append(args, command...)
	return args
}

func (g *commandStartGate) Attach(cmd *exec.Cmd) {
	if g == nil || g.readPipe == nil {
		return
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, g.readPipe)
}

func (g *commandStartGate) AfterStart() error {
	if g == nil || g.readPipe == nil {
		return nil
	}
	err := g.readPipe.Close()
	g.readPipe = nil
	return err
}

func (g *commandStartGate) Release() error {
	if g == nil {
		return nil
	}
	var releaseErr error
	g.release.Do(func() {
		if g.writePipe != nil {
			if _, err := g.writePipe.Write([]byte{'\n'}); err != nil {
				releaseErr = err
			}
			if err := g.writePipe.Close(); err != nil && releaseErr == nil {
				releaseErr = err
			}
			g.writePipe = nil
		}
	})
	return releaseErr
}

func (g *commandStartGate) Close() error {
	if g == nil {
		return nil
	}
	var closeErr error
	g.close.Do(func() {
		if g.readPipe != nil {
			if err := g.readPipe.Close(); err != nil {
				closeErr = err
			}
			g.readPipe = nil
		}
		if g.writePipe != nil {
			if err := g.writePipe.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
			g.writePipe = nil
		}
	})
	return closeErr
}
