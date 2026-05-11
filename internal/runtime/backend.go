package runtime

import (
	"context"
	"time"
)

const (
	SkillSourceBuiltin = "builtin"
	SkillSourceShared  = "shared"
	SkillSourceMapped  = "mapped"
)

const (
	SkillTrustLevelBuiltin     = "builtin"
	SkillTrustLevelDistributed = "distributed"
	SkillTrustLevelLearned     = "learned"
)

type ExecutionSpec struct {
	Command         []string
	Env             map[string]string
	Timeout         time.Duration
	Workspace       string
	Skill           string
	SkillSource     string
	SkillTrustLevel string
}

type RuntimeBackend interface {
	Name() string
	Execute(ctx context.Context, spec ExecutionSpec) (ExecResult, error)
}

type ExecutionMetadata struct {
	Backend         string `json:"backend,omitempty"`
	Skill           string `json:"skill,omitempty"`
	SkillSource     string `json:"skillSource,omitempty"`
	SkillTrustLevel string `json:"skillTrustLevel,omitempty"`
}

type SkillInfo struct {
	Name          string   `json:"name"`
	Source        string   `json:"source,omitempty"`
	TrustLevel    string   `json:"trustLevel,omitempty"`
	RootDir       string   `json:"rootDir,omitempty"`
	ActiveDir     string   `json:"activeDir,omitempty"`
	CLIPath       string   `json:"cliPath,omitempty"`
	Actions       []string `json:"actions,omitempty"`
	Version       string   `json:"version,omitempty"`
	Versions      []string `json:"versions,omitempty"`
	Enabled       bool     `json:"enabled"`
	ScanVerdict   string   `json:"scanVerdict,omitempty"`
	BlockedReason string   `json:"blockedReason,omitempty"`
}
