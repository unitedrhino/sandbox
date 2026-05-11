package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

func TestPrepare(t *testing.T) {
	storeRoot := t.TempDir()
	mustWriteDir(t, filepath.Join(storeRoot, "common", "ur-api"))
	mustWriteDir(t, filepath.Join(storeRoot, "shared", "team-skill"))
	mustWriteDir(t, filepath.Join(storeRoot, "mapped", "demo-skill"))
	runtimeRoot := t.TempDir()
	cfg := config.Config{
		Workspace:              filepath.Join(t.TempDir(), "workspace"),
		ControlDir:             filepath.Join(t.TempDir(), "control"),
		SkillStoreCommonRoot:   filepath.Join(storeRoot, "common"),
		SkillStoreSharedRoot:   filepath.Join(storeRoot, "shared"),
		SkillStoreMappedRoot:   filepath.Join(storeRoot, "mapped"),
		RuntimeSkillCommonRoot: filepath.Join(runtimeRoot, "common"),
		RuntimeSkillSharedRoot: filepath.Join(runtimeRoot, "shared"),
		RuntimeSkillMappedRoot: filepath.Join(runtimeRoot, "mapped"),
	}

	state, cleanup, err := Prepare(cfg)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer cleanup()

	if state.MainPIDPath == "" {
		t.Fatal("MainPIDPath should not be empty")
	}
	if _, err := os.Stat(state.MainPIDPath); err != nil {
		t.Fatalf("main pid file not created: %v", err)
	}
	assertSymlinkTarget(t, filepath.Join(cfg.RuntimeSkillCommonRoot, "ur-api"), filepath.Join(cfg.SkillStoreCommonRoot, "ur-api"))
	assertSymlinkTarget(t, filepath.Join(cfg.RuntimeSkillSharedRoot, "team-skill"), filepath.Join(cfg.SkillStoreSharedRoot, "team-skill"))
	assertSymlinkTarget(t, filepath.Join(cfg.RuntimeSkillMappedRoot, "demo-skill"), filepath.Join(cfg.SkillStoreMappedRoot, "demo-skill"))
}

func mustWriteDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func assertSymlinkTarget(t *testing.T, linkPath, want string) {
	t.Helper()
	got, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink %s: %v", linkPath, err)
	}
	if got != want {
		t.Fatalf("symlink %s = %s, want %s", linkPath, got, want)
	}
}
