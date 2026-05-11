package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gitee.com/unitedrhino/sandbox/internal/config"
)

type RuntimeEngine struct {
	opts             config.ExecOptions
	builtinSkillRoot string
	sharedSkillRoot  string
	mappedSkillRoot  string
	skillRunner      SkillRunner
	directBackend    RuntimeBackend
	sandboxBackend   RuntimeBackend
}

func NewRuntimeEngine(opts config.ExecOptions) *RuntimeEngine {
	builtinRoot := opts.BuiltinSkillRoot
	if strings.TrimSpace(builtinRoot) == "" {
		builtinRoot = DefaultBuiltinSkillRoot
	}
	sharedRoot := opts.SharedSkillRoot
	if strings.TrimSpace(sharedRoot) == "" {
		sharedRoot = DefaultSharedSkillRoot
	}
	mappedRoot := opts.MappedSkillRoot
	if strings.TrimSpace(mappedRoot) == "" {
		mappedRoot = DefaultMappedSkillRoot
	}
	engine := &RuntimeEngine{
		opts:             opts,
		builtinSkillRoot: builtinRoot,
		sharedSkillRoot:  sharedRoot,
		mappedSkillRoot:  mappedRoot,
		directBackend:    NewDirectBackend(opts),
		sandboxBackend:   NewSandboxBackend(opts),
	}
	engine.reloadSkillRunner()
	return engine
}

func (e *RuntimeEngine) Execute(ctx context.Context, req ExecRequest) (ExecResult, error) {
	spec, err := e.skillRunner.Resolve(req)
	if err != nil {
		return ExecResult{}, err
	}
	return e.selectBackend().Execute(ctx, spec)
}

func (e *RuntimeEngine) Describe(req ExecRequest) (ExecutionMetadata, error) {
	spec, err := e.skillRunner.Resolve(req)
	if err != nil {
		return ExecutionMetadata{}, err
	}
	return ExecutionMetadata{
		Backend:         e.selectBackend().Name(),
		Skill:           spec.Skill,
		SkillSource:     spec.SkillSource,
		SkillTrustLevel: spec.SkillTrustLevel,
	}, nil
}

func (e *RuntimeEngine) ListSkills() []SkillInfo {
	if lister, ok := e.skillRunner.(interface{ ListSkills() []SkillInfo }); ok {
		return lister.ListSkills()
	}
	return nil
}

func (e *RuntimeEngine) GetSkill(name string) (SkillInfo, bool) {
	if getter, ok := e.skillRunner.(interface {
		GetSkill(string) (SkillInfo, bool)
	}); ok {
		return getter.GetSkill(name)
	}
	return SkillInfo{}, false
}

func (e *RuntimeEngine) ReloadSkills() error {
	e.reloadSkillRunner()
	return nil
}

func (e *RuntimeEngine) ActivateSkill(name, version string) (SkillInfo, error) {
	skill, ok := e.GetSkill(name)
	if !ok {
		return SkillInfo{}, fmt.Errorf("skill not found: %s", name)
	}
	if skill.Source == "builtin" {
		return SkillInfo{}, fmt.Errorf("builtin skill cannot be activated: %s", name)
	}
	if strings.TrimSpace(version) == "" {
		return SkillInfo{}, fmt.Errorf("version is required")
	}
	if !slices.Contains(skill.Versions, version) {
		return SkillInfo{}, fmt.Errorf("version not found: %s", version)
	}

	targetDir := filepath.Join(skill.RootDir, "versions", version)
	guard := ScanMappedSkill(targetDir)
	if !guard.Enabled {
		return SkillInfo{}, fmt.Errorf("skill version blocked: %s", guard.BlockedReason)
	}

	currentPath, err := e.skillCurrentPath(skill)
	if err != nil {
		return SkillInfo{}, err
	}
	previous := strings.TrimSpace(readFileString(currentPath))
	if err := os.WriteFile(currentPath, []byte(version), 0o644); err != nil {
		return SkillInfo{}, err
	}
	e.reloadSkillRunner()
	updated, ok := e.GetSkill(name)
	if !ok || updated.Version != version || !updated.Enabled {
		rollback := previous
		if rollback == "" {
			rollback = skill.Version
		}
		_ = os.WriteFile(currentPath, []byte(rollback), 0o644)
		e.reloadSkillRunner()
		if !ok {
			return SkillInfo{}, fmt.Errorf("skill not found after activate: %s", name)
		}
		return SkillInfo{}, fmt.Errorf("activated skill is not runnable: %s", name)
	}
	return updated, nil
}

func (e *RuntimeEngine) reloadSkillRunner() {
	e.skillRunner = NewBuiltinSkillRunner(NewCombinedSkillCatalog(e.builtinSkillRoot, e.sharedSkillRoot, e.mappedSkillRoot, e.opts.ControlDir))
}

func (e *RuntimeEngine) skillCurrentPath(skill SkillInfo) (string, error) {
	if strings.TrimSpace(e.opts.ControlDir) == "" {
		return "", fmt.Errorf("control dir is required for skill activation")
	}
	base := filepath.Join(e.opts.ControlDir, "skills-state", skill.Source, skill.Name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(base, "current"), nil
}

func (e *RuntimeEngine) selectBackend() RuntimeBackend {
	if e.opts.MountSandboxEnable || e.opts.SandboxNetEnable || e.opts.CPUQuota > 0 || e.opts.MemoryLimitMB > 0 || e.opts.MaxProcesses > 0 {
		return e.sandboxBackend
	}
	return e.directBackend
}
