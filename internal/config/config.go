package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	ListenAddr             string
	RuntimeID              string
	TenantCode             string
	AgentID                string
	CloneID                string
	CloneKey               string
	SessionID              string
	WorkspaceRoot          string
	Workspace              string
	ControlDir             string
	WorkspaceHostRoot      string
	WorkspaceHost          string
	AisvrBaseURL           string
	AisvrToken             string
	EnableSandboxNet       bool
	EnableMountSandbox     bool
	ProxyPort              int
	BlockedCIDRs           []string
	AllowedCIDRs           []string
	AllowedPorts           []int
	AllowedInternalTargets []string
	CPUQuota               int64
	MemoryLimitMB          int64
	MaxProcesses           int64
	IdleTimeout            time.Duration
	RunnerUID              uint32
	RunnerGID              uint32
	SkillStoreCommonRoot   string
	SkillStoreSharedRoot   string
	SkillStoreMappedRoot   string
	RuntimeSkillCommonRoot string
	RuntimeSkillSharedRoot string
	RuntimeSkillMappedRoot string
	TaskBaseEnv            map[string]string
}

func LoadFromEnv() (Config, error) {
	workspaceRoot := strings.TrimSpace(os.Getenv("CLAW_WORKSPACE"))
	workspaceHostRoot := strings.TrimSpace(os.Getenv("CLAW_WORKSPACE_HOST_PATH"))
	cfg := Config{
		ListenAddr:             getEnvDefault("CLAW_LISTEN_ADDR", ":8080"),
		RuntimeID:              strings.TrimSpace(os.Getenv("CLAW_RUNTIME_ID")),
		TenantCode:             strings.TrimSpace(os.Getenv("CLAW_TENANT_CODE")),
		AgentID:                strings.TrimSpace(os.Getenv("CLAW_AGENT_ID")),
		CloneID:                strings.TrimSpace(os.Getenv("CLAW_CLONE_ID")),
		CloneKey:               strings.TrimSpace(os.Getenv("CLAW_CLONE_KEY")),
		SessionID:              strings.TrimSpace(os.Getenv("CLAW_SESSION_ID")),
		WorkspaceRoot:          workspaceRoot,
		ControlDir:             getEnvDefault("CLAW_CONTROL_DIR", "/runtime/control"),
		WorkspaceHostRoot:      workspaceHostRoot,
		AisvrBaseURL:           strings.TrimSpace(os.Getenv("CLAW_AISVR_BASE_URL")),
		AisvrToken:             strings.TrimSpace(os.Getenv("CLAW_AISVR_TOKEN")),
		ProxyPort:              getEnvInt("CLAW_SANDBOX_PROXY_PORT", 1080),
		BlockedCIDRs:           getEnvCSV("CLAW_SANDBOX_BLOCKED_CIDRS"),
		AllowedCIDRs:           getEnvCSV("CLAW_SANDBOX_ALLOWED_CIDRS"),
		AllowedPorts:           getEnvIntCSV("CLAW_SANDBOX_ALLOWED_PORTS"),
		AllowedInternalTargets: getEnvCSV("CLAW_ALLOWED_INTERNAL_TARGETS"),
		CPUQuota:               getEnvInt64("CLAW_CPU_LIMIT", 50),
		MemoryLimitMB:          getEnvInt64("CLAW_MEMORY_LIMIT_MB", 512),
		MaxProcesses:           getEnvInt64("CLAW_MAX_PROCESSES", 64),
		IdleTimeout:            time.Duration(getEnvInt("CLAW_IDLE_TIMEOUT_SEC", 10)) * time.Second,
		RunnerUID:              uint32(getEnvInt("CLAW_RUNNER_UID", 10001)),
		RunnerGID:              uint32(getEnvInt("CLAW_RUNNER_GID", 10001)),
		SkillStoreCommonRoot:   getEnvDefault("CLAW_SKILL_STORE_COMMON_ROOT", "/opt/skills-store/common"),
		SkillStoreSharedRoot:   getEnvDefault("CLAW_SKILL_STORE_SHARED_ROOT", "/opt/skills-store/shared"),
		SkillStoreMappedRoot:   getEnvDefault("CLAW_SKILL_STORE_MAPPED_ROOT", "/opt/skills-store/mapped"),
		RuntimeSkillCommonRoot: getEnvDefault("CLAW_RUNTIME_SKILL_COMMON_ROOT", "/runtime/skills/common"),
		RuntimeSkillSharedRoot: getEnvDefault("CLAW_RUNTIME_SKILL_SHARED_ROOT", "/runtime/skills/shared"),
		RuntimeSkillMappedRoot: getEnvDefault("CLAW_RUNTIME_SKILL_MAPPED_ROOT", "/runtime/skills/mapped"),
		TaskBaseEnv:            collectTaskBaseEnv(),
	}
	// 网络隔离为强制安全要求，默认启用。环境变量仅用于紧急关闭。
	cfg.EnableSandboxNet = getEnvBool("CLAW_ENABLE_SANDBOX_NET", true)
	cfg.EnableMountSandbox = getEnvBool("CLAW_ENABLE_MOUNT_SANDBOX", false)

	for _, field := range []struct {
		name  string
		value string
	}{
		{"CLAW_RUNTIME_ID", cfg.RuntimeID},
		{"CLAW_TENANT_CODE", cfg.TenantCode},
		{"CLAW_CLONE_ID", cfg.CloneID},
		{"CLAW_CLONE_KEY", cfg.CloneKey},
		{"CLAW_WORKSPACE", cfg.WorkspaceRoot},
	} {
		if field.value == "" {
			return Config{}, fmt.Errorf("%s is required", field.name)
		}
	}

	workspace, err := deriveCloneWorkspacePath(cfg.WorkspaceRoot, cfg.TenantCode, cfg.CloneKey, cfg.CloneID)
	if err != nil {
		return Config{}, err
	}
	cfg.Workspace = workspace
	if cfg.WorkspaceHostRoot != "" {
		workspaceHost, err := deriveCloneWorkspacePath(cfg.WorkspaceHostRoot, cfg.TenantCode, cfg.CloneKey, cfg.CloneID)
		if err != nil {
			return Config{}, err
		}
		cfg.WorkspaceHost = workspaceHost
	}

	return cfg, nil
}

func deriveCloneWorkspacePath(root, tenantCode, cloneKey, cloneID string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("CLAW_WORKSPACE is required")
	}
	tenantSegment := sanitizeWorkspaceSegment(tenantCode)
	if tenantSegment == "" {
		return "", fmt.Errorf("CLAW_TENANT_CODE resolves to an empty workspace segment")
	}
	cloneIDSegment := sanitizeWorkspaceSegment(cloneID)
	if cloneIDSegment == "" {
		return "", fmt.Errorf("CLAW_CLONE_ID resolves to an empty workspace segment")
	}
	cloneSegment := sanitizeWorkspaceSegment(cloneKey)
	segments := []string{filepath.Clean(root), tenantSegment}
	if cloneSegment != "" {
		segments = append(segments, cloneSegment)
	}
	segments = append(segments, cloneIDSegment, "work")
	return filepath.Join(segments...), nil
}

func sanitizeWorkspaceSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out []rune
	lastUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			out = append(out, r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			out = append(out, '_')
			lastUnderscore = true
		}
	}
	return strings.Trim(string(out), "._-")
}

type ExecOptions struct {
	RunnerUID              uint32
	RunnerGID              uint32
	ControlDir             string
	BuiltinSkillRoot       string
	SharedSkillRoot        string
	MappedSkillRoot        string
	SandboxNetEnable       bool
	MountSandboxEnable     bool
	SandboxProxyPort       int
	BlockedCIDRs           []string
	AllowedCIDRs           []string
	AllowedPorts           []int
	AllowedInternalTargets []string
	CPUQuota               int64
	MemoryLimitMB          int64
	MaxProcesses           int64
}

func getEnvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes"
}

func getEnvCSV(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getEnvIntCSV(key string) []int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if parsed, err := strconv.Atoi(part); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}

func collectTaskBaseEnv() map[string]string {
	keys := []string{
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"OPENAI_MODEL",
		"UR_BASE_URL",
		"UR_APP_ID",
		"UR_TENANT_CODE",
		"UR_ACCOUNT",
		"UR_PASSWORD",
		"DEEPSEEK_API_KEY",
		"DEEPSEEK_BASE_URL",
		"DEEPSEEK_MODEL",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_MODEL",
		"GEMINI_API_KEY",
		"GEMINI_BASE_URL",
		"GEMINI_MODEL",
		"AZURE_OPENAI_API_KEY",
		"AZURE_OPENAI_BASE_URL",
		"AZURE_OPENAI_MODEL",
	}
	keys = append(keys, getEnvCSV("CLAW_TASK_ENV_KEYS")...)
	out := make(map[string]string)
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			out[key] = value
		}
	}
	return out
}
