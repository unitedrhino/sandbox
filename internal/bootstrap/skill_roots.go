package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

func prepareSkillRoots(cfg config.Config) error {
	pairs := []struct {
		store   string
		runtime string
	}{
		{store: cfg.SkillStoreCommonRoot, runtime: cfg.RuntimeSkillCommonRoot},
		{store: cfg.SkillStoreSharedRoot, runtime: cfg.RuntimeSkillSharedRoot},
		{store: cfg.SkillStoreMappedRoot, runtime: cfg.RuntimeSkillMappedRoot},
	}

	for _, pair := range pairs {
		if err := os.MkdirAll(pair.runtime, 0o755); err != nil {
			return fmt.Errorf("mkdir runtime skill root %s: %w", pair.runtime, err)
		}
		if err := clearDir(pair.runtime); err != nil {
			return fmt.Errorf("clear runtime skill root %s: %w", pair.runtime, err)
		}
		if stringsEmpty(pair.store) {
			continue
		}
		entries, err := os.ReadDir(pair.store)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read skill store root %s: %w", pair.store, err)
		}
		for _, entry := range entries {
			target := filepath.Join(pair.store, entry.Name())
			destPath := filepath.Join(pair.runtime, entry.Name())
			if err := copyDir(target, destPath); err != nil {
				return fmt.Errorf("copy %s -> %s: %w", target, destPath, err)
			}
		}
	}

	return nil
}

func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0o644)
	})
}

func stringsEmpty(v string) bool {
	return len(v) == 0
}
