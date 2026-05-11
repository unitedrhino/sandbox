package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type State struct {
	MainPIDPath string
}

func Prepare(cfg config.Config) (State, func(), error) {
	if err := os.MkdirAll(cfg.Workspace, 0o755); err != nil {
		return State{}, nil, fmt.Errorf("mkdir workspace: %w", err)
	}
	if err := os.MkdirAll(cfg.ControlDir, 0o755); err != nil {
		return State{}, nil, fmt.Errorf("mkdir control dir: %w", err)
	}
	if err := prepareSkillRoots(cfg); err != nil {
		return State{}, nil, fmt.Errorf("prepare skill roots: %w", err)
	}

	pidPath := filepath.Join(cfg.ControlDir, "main.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return State{}, nil, fmt.Errorf("write main pid: %w", err)
	}

	if os.Geteuid() == 0 {
		_ = os.Chown(cfg.Workspace, int(cfg.RunnerUID), int(cfg.RunnerGID))
		_ = os.Chmod(cfg.Workspace, 0o755)
		_ = os.Chown(cfg.ControlDir, 0, 0)
		_ = os.Chmod(cfg.ControlDir, 0o555)
		_ = os.Chown(pidPath, 0, 0)
		_ = os.Chmod(pidPath, 0o444)
	}

	cleanup := func() {
		_ = os.Remove(pidPath)
	}

	return State{MainPIDPath: pidPath}, cleanup, nil
}
