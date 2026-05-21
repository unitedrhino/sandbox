package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	DefaultBuiltinSkillRoot = "/runtime/skills/common"
	DefaultSharedSkillRoot  = "/runtime/skills/shared"
	DefaultMappedSkillRoot  = "/runtime/skills/mapped"
	UrAPICLIPath            = "/usr/local/bin/ur-api"
)

type BuiltinSkill struct {
	Name          string
	Source        string
	TrustLevel    string
	RootDir       string
	ActiveDir     string
	CLIPath       string
	Actions       []string
	Version       string
	Versions      []string
	Enabled       bool
	ScanVerdict   string
	BlockedReason string
}

type BuiltinSkillCatalog struct {
	skills map[string]BuiltinSkill
}

func defaultBuiltinSkillDefinitions() map[string]BuiltinSkill {
	return map[string]BuiltinSkill{
		"ur-api": {
			Name:        "ur-api",
			Source:      SkillSourceBuiltin,
			TrustLevel:  SkillTrustLevelBuiltin,
			RootDir:     DefaultBuiltinSkillRoot + "/ur-api",
			ActiveDir:   DefaultBuiltinSkillRoot + "/ur-api",
			CLIPath:     UrAPICLIPath,
			Actions:     []string{"check", "get-self"},
			Enabled:     true,
			ScanVerdict: "safe",
		},
	}
}

func DefaultBuiltinSkillCatalog() BuiltinSkillCatalog {
	return BuiltinSkillCatalog{
		skills: defaultBuiltinSkillDefinitions(),
	}
}

func NewCombinedSkillCatalog(builtinRoot, sharedRoot, mappedRoot, controlDir string) BuiltinSkillCatalog {
	out := make(map[string]BuiltinSkill)

	for name, skill := range NewBuiltinSkillCatalogFromRoot(builtinRoot).skills {
		out[name] = skill
	}
	for name, skill := range NewSharedSkillCatalogFromRoot(sharedRoot, controlDir).skills {
		out[name] = skill
	}
	for name, skill := range NewMappedSkillCatalogFromRoot(mappedRoot, controlDir).skills {
		if _, exists := out[name]; exists {
			continue
		}
		out[name] = skill
	}
	return BuiltinSkillCatalog{skills: out}
}

func NewSharedSkillCatalogFromRoot(root, controlDir string) BuiltinSkillCatalog {
	return newExternalSkillCatalogFromRoot(root, SkillSourceShared, SkillTrustLevelDistributed, controlDir)
}

func NewBuiltinSkillCatalogFromRoot(root string) BuiltinSkillCatalog {
	definitions := defaultBuiltinSkillDefinitions()
	if strings.TrimSpace(root) == "" {
		return BuiltinSkillCatalog{skills: definitions}
	}

	out := make(map[string]BuiltinSkill)
	for name, skill := range definitions {
		skillDir := filepath.Join(root, name)
		if !fileExists(filepath.Join(skillDir, "SKILL.md")) {
			continue
		}
		skill.RootDir = skillDir
		out[name] = skill
	}
	return BuiltinSkillCatalog{skills: out}
}

func NewMappedSkillCatalogFromRoot(root, controlDir string) BuiltinSkillCatalog {
	return newExternalSkillCatalogFromRoot(root, SkillSourceMapped, SkillTrustLevelLearned, controlDir)
}

func newExternalSkillCatalogFromRoot(root, source, trustLevel, controlDir string) BuiltinSkillCatalog {
	if strings.TrimSpace(root) == "" {
		return BuiltinSkillCatalog{skills: map[string]BuiltinSkill{}}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return BuiltinSkillCatalog{skills: map[string]BuiltinSkill{}}
	}

	out := make(map[string]BuiltinSkill)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		skillDir := filepath.Join(root, name)
		activeDir, version, versions, ok := resolveExternalSkillActiveDir(skillDir, source, name, controlDir)
		if !ok {
			continue
		}
		actions := detectMappedSkillActions(activeDir)
		if len(actions) == 0 {
			continue
		}
		guard := ScanMappedSkill(activeDir)
		out[name] = BuiltinSkill{
			Name:          name,
			Source:        source,
			TrustLevel:    trustLevel,
			RootDir:       skillDir,
			ActiveDir:     activeDir,
			Actions:       actions,
			Version:       version,
			Versions:      versions,
			Enabled:       guard.Enabled,
			ScanVerdict:   guard.ScanVerdict,
			BlockedReason: guard.BlockedReason,
		}
	}
	return BuiltinSkillCatalog{skills: out}
}

func (c BuiltinSkillCatalog) Lookup(name string) (BuiltinSkill, bool) {
	skill, ok := c.skills[name]
	return skill, ok
}

func (c BuiltinSkillCatalog) Get(name string) (SkillInfo, bool) {
	skill, ok := c.skills[name]
	if !ok {
		return SkillInfo{}, false
	}
	return SkillInfo{
		Name:          skill.Name,
		Source:        skill.Source,
		TrustLevel:    skill.TrustLevel,
		RootDir:       skill.RootDir,
		ActiveDir:     skill.ActiveDir,
		CLIPath:       skill.CLIPath,
		Actions:       slices.Clone(skill.Actions),
		Version:       skill.Version,
		Versions:      slices.Clone(skill.Versions),
		Enabled:       skill.Enabled,
		ScanVerdict:   skill.ScanVerdict,
		BlockedReason: skill.BlockedReason,
	}, true
}

func (c BuiltinSkillCatalog) List() []SkillInfo {
	names := make([]string, 0, len(c.skills))
	for name := range c.skills {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]SkillInfo, 0, len(names))
	for _, name := range names {
		skill := c.skills[name]
		out = append(out, SkillInfo{
			Name:          skill.Name,
			Source:        skill.Source,
			TrustLevel:    skill.TrustLevel,
			RootDir:       skill.RootDir,
			ActiveDir:     skill.ActiveDir,
			CLIPath:       skill.CLIPath,
			Actions:       slices.Clone(skill.Actions),
			Version:       skill.Version,
			Versions:      slices.Clone(skill.Versions),
			Enabled:       skill.Enabled,
			ScanVerdict:   skill.ScanVerdict,
			BlockedReason: skill.BlockedReason,
		})
	}
	return out
}

type SkillRunner interface {
	Resolve(req ExecRequest) (ExecutionSpec, error)
}

type BuiltinSkillRunner struct {
	catalog BuiltinSkillCatalog
}

func NewBuiltinSkillRunner(catalog BuiltinSkillCatalog) *BuiltinSkillRunner {
	return &BuiltinSkillRunner{catalog: catalog}
}

func (r *BuiltinSkillRunner) ListSkills() []SkillInfo {
	return r.catalog.List()
}

func (r *BuiltinSkillRunner) GetSkill(name string) (SkillInfo, bool) {
	return r.catalog.Get(name)
}

func (r *BuiltinSkillRunner) Resolve(req ExecRequest) (ExecutionSpec, error) {
	spec := ExecutionSpec{
		Command:   slices.Clone(req.Command),
		Env:       cloneEnv(req.Env),
		Timeout:   req.Timeout,
		Workspace: req.Workspace,
	}
	if len(spec.Command) == 0 {
		return spec, nil
	}

	switch spec.Command[0] {
	case "sandbox-skill":
		return r.resolveSandboxSkill(spec)
	case "ur-api":
		skill, ok := r.catalog.Lookup("ur-api")
		if !ok {
			return ExecutionSpec{}, fmt.Errorf("builtin skill not found: ur-api")
		}
		spec.Command = append([]string{skill.CLIPath}, spec.Command[1:]...)
		spec.Skill = skill.Name
		spec.SkillSource = skill.Source
		spec.SkillTrustLevel = skill.TrustLevel
		return spec, nil
	default:
		return spec, nil
	}
}

func (r *BuiltinSkillRunner) resolveSandboxSkill(spec ExecutionSpec) (ExecutionSpec, error) {
	if len(spec.Command) < 3 {
		return ExecutionSpec{}, fmt.Errorf("usage: sandbox-skill <skill> <command>")
	}

	skillName := spec.Command[1]
	action := spec.Command[2]
	args := spec.Command[3:]

	skill, ok := r.catalog.Lookup(skillName)
	if !ok {
		return ExecutionSpec{}, fmt.Errorf("unsupported skill: %s", skillName)
	}
	if !skill.Enabled {
		return ExecutionSpec{}, fmt.Errorf("skill blocked: %s", skill.BlockedReason)
	}

	switch skillName {
	case "ur-api":
		spec.Skill = skill.Name
		spec.SkillSource = skill.Source
		spec.SkillTrustLevel = skill.TrustLevel
		switch action {
		case "check":
			spec.Command = append([]string{skill.CLIPath, "check"}, args...)
		case "get-self":
			spec.Command = append([]string{
				skill.CLIPath,
				"api",
				"/api/v1/system/user/self/get-one",
				"--body",
				`{"withTenant":true}`,
			}, args...)
		default:
			spec.Command = append([]string{skill.CLIPath, action}, args...)
		}
		return spec, nil
	default:
		return r.resolveMappedSkill(spec, skill, action, args)
	}
}

func (r *BuiltinSkillRunner) resolveMappedSkill(spec ExecutionSpec, skill BuiltinSkill, action string, args []string) (ExecutionSpec, error) {
	spec.Skill = skill.Name
	spec.SkillSource = skill.Source
	spec.SkillTrustLevel = skill.TrustLevel

	scriptBase := filepath.Join(skill.ActiveDir, "scripts", action)
	for _, ext := range []string{".sh", ".js", ".py", ""} {
		path := scriptBase + ext
		if fileExists(path) {
			spec.Command = append([]string{path}, args...)
			return spec, nil
		}
	}
	return ExecutionSpec{}, fmt.Errorf("unsupported skill action: %s/%s", skill.Name, action)
}

func compareVersionLike(a, b string) int {
	ai := versionSortKey(a)
	bi := versionSortKey(b)
	limit := len(ai)
	if len(bi) > limit {
		limit = len(bi)
	}
	for i := 0; i < limit; i++ {
		if i >= len(ai) {
			return -1
		}
		if i >= len(bi) {
			return 1
		}
		if ai[i] < bi[i] {
			return -1
		}
		if ai[i] > bi[i] {
			return 1
		}
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func versionSortKey(value string) []int {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'z')
	})
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n := 0
		for _, ch := range part {
			if ch >= '0' && ch <= '9' {
				n = n*10 + int(ch-'0')
			} else {
				n = n*100 + int(ch)
			}
		}
		out = append(out, n)
	}
	return out
}

func resolveExternalSkillActiveDir(skillDir, source, name, controlDir string) (activeDir, version string, versions []string, ok bool) {
	if fileExists(filepath.Join(skillDir, "SKILL.md")) {
		return skillDir, "", nil, true
	}

	versionsDir := filepath.Join(skillDir, "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return "", "", nil, false
	}
	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(versionsDir, entry.Name(), "SKILL.md")) {
			versions = append(versions, entry.Name())
		}
	}
	slices.SortFunc(versions, compareVersionLike)
	if len(versions) == 0 {
		return "", "", nil, false
	}

	current := strings.TrimSpace(readFileString(skillStateCurrentPath(controlDir, source, name)))
	if current == "" {
		current = strings.TrimSpace(readFileString(filepath.Join(skillDir, "current")))
	}
	if current == "" {
		current = versions[len(versions)-1]
	}
	activeDir = filepath.Join(versionsDir, current)
	if !fileExists(filepath.Join(activeDir, "SKILL.md")) {
		return "", "", versions, false
	}
	return activeDir, current, versions, true
}

func skillStateCurrentPath(controlDir, source, name string) string {
	if controlDir == "" {
		return ""
	}
	return filepath.Join(controlDir, "skills-state", source, name, "current")
}

func cloneEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func detectMappedSkillActions(skillDir string) []string {
	scriptsDir := filepath.Join(skillDir, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var actions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if base == "" {
			continue
		}
		switch ext {
		case ".sh", ".js", ".py", "":
		default:
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		actions = append(actions, base)
	}
	slices.Sort(actions)
	return actions
}
