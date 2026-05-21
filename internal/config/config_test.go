package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromEnv_Success(t *testing.T) {
	t.Setenv("SANDBOX_RUNTIME_ID", "rt-1")
	t.Setenv("SANDBOX_TENANT_CODE", "tenant-a")
	t.Setenv("SANDBOX_AGENT_ID", "agent-1")
	t.Setenv("SANDBOX_CLONE_ID", "clone-1")
	t.Setenv("SANDBOX_CLONE_KEY", "clone-key")
	t.Setenv("SANDBOX_WORKSPACE", "/workspace")
	t.Setenv("SANDBOX_CONTROL_DIR", "/runtime/control")
	t.Setenv("SANDBOX_LISTEN_ADDR", ":19090")
	t.Setenv("SANDBOX_ENABLE_SANDBOX_NET", "true")
	t.Setenv("SANDBOX_ENABLE_MOUNT_SANDBOX", "true")
	t.Setenv("SANDBOX_PROXY_PORT", "2080")
	t.Setenv("SANDBOX_BLOCKED_CIDRS", "10.0.0.0/8,172.16.0.0/12")
	t.Setenv("SANDBOX_ALLOWED_CIDRS", "192.168.32.1/32,8.8.8.8/32")
	t.Setenv("SANDBOX_ALLOWED_PORTS", "443,19091")
	t.Setenv("SANDBOX_ALLOWED_INTERNAL_TARGETS", "allinone:7777,10.1.2.3:7777")
	t.Setenv("SANDBOX_MAX_PROCESSES", "32")
	t.Setenv("SANDBOX_RUNNER_UID", "20001")
	t.Setenv("SANDBOX_RUNNER_GID", "20002")
	t.Setenv("SANDBOX_SKILL_STORE_COMMON_ROOT", "/opt/skills-store/common")
	t.Setenv("SANDBOX_SKILL_STORE_SHARED_ROOT", "/opt/skills-store/shared")
	t.Setenv("SANDBOX_SKILL_STORE_MAPPED_ROOT", "/opt/skills-store/mapped")
	t.Setenv("SANDBOX_RUNTIME_SKILL_COMMON_ROOT", "/runtime/skills/common")
	t.Setenv("SANDBOX_RUNTIME_SKILL_SHARED_ROOT", "/runtime/skills/shared")
	t.Setenv("SANDBOX_RUNTIME_SKILL_MAPPED_ROOT", "/runtime/skills/mapped")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_MODEL", "gpt-test")
	t.Setenv("UR_BASE_URL", "http://mock-ur")
	t.Setenv("UR_APP_ID", "77")
	t.Setenv("UR_TENANT_CODE", "default")
	t.Setenv("UR_ACCOUNT", "administrator")
	t.Setenv("UR_PASSWORD", "iThings666")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RuntimeID != "rt-1" {
		t.Fatalf("RuntimeID = %s", cfg.RuntimeID)
	}
	if cfg.ListenAddr != ":19090" {
		t.Fatalf("ListenAddr = %s", cfg.ListenAddr)
	}
	if cfg.AgentID != "agent-1" {
		t.Fatalf("AgentID = %s", cfg.AgentID)
	}
	if cfg.ControlDir != "/runtime/control" {
		t.Fatalf("ControlDir = %s", cfg.ControlDir)
	}
	if cfg.WorkspaceRoot != "/workspace" {
		t.Fatalf("WorkspaceRoot = %s", cfg.WorkspaceRoot)
	}
	if cfg.Workspace != "/workspace/tenant-a/clone-key/clone-1/work" {
		t.Fatalf("Workspace = %s", cfg.Workspace)
	}
	if !cfg.EnableSandboxNet {
		t.Fatal("EnableSandboxNet should be true")
	}
	if cfg.ProxyPort != 2080 {
		t.Fatalf("ProxyPort = %d", cfg.ProxyPort)
	}
	if cfg.MaxProcesses != 32 {
		t.Fatalf("MaxProcesses = %d", cfg.MaxProcesses)
	}
	if cfg.RunnerUID != 20001 || cfg.RunnerGID != 20002 {
		t.Fatalf("runner uid/gid = %d/%d", cfg.RunnerUID, cfg.RunnerGID)
	}
	if cfg.SkillStoreCommonRoot != "/opt/skills-store/common" || cfg.RuntimeSkillMappedRoot != "/runtime/skills/mapped" {
		t.Fatalf("skill roots = %+v", cfg)
	}
	if !cfg.EnableMountSandbox {
		t.Fatal("EnableMountSandbox should be true")
	}
	if len(cfg.BlockedCIDRs) != 2 || cfg.BlockedCIDRs[0] != "10.0.0.0/8" {
		t.Fatalf("blocked cidrs = %#v", cfg.BlockedCIDRs)
	}
	if len(cfg.AllowedCIDRs) != 2 || cfg.AllowedCIDRs[0] != "192.168.32.1/32" {
		t.Fatalf("allowed cidrs = %#v", cfg.AllowedCIDRs)
	}
	if len(cfg.AllowedPorts) != 2 || cfg.AllowedPorts[0] != 443 || cfg.AllowedPorts[1] != 19091 {
		t.Fatalf("allowed ports = %#v", cfg.AllowedPorts)
	}
	if len(cfg.AllowedInternalTargets) != 2 || cfg.AllowedInternalTargets[0] != "allinone:7777" {
		t.Fatalf("allowed internal targets = %#v", cfg.AllowedInternalTargets)
	}
	if cfg.TaskBaseEnv["OPENAI_API_KEY"] != "sk-test" || cfg.TaskBaseEnv["OPENAI_MODEL"] != "gpt-test" {
		t.Fatalf("task base env = %#v", cfg.TaskBaseEnv)
	}
	if cfg.TaskBaseEnv["UR_BASE_URL"] != "http://mock-ur" {
		t.Fatalf("UR_BASE_URL = %q", cfg.TaskBaseEnv["UR_BASE_URL"])
	}
	if cfg.TaskBaseEnv["UR_ACCOUNT"] != "administrator" {
		t.Fatalf("UR_ACCOUNT = %q", cfg.TaskBaseEnv["UR_ACCOUNT"])
	}
}

func TestLoadFromEnv_DerivesWorkspaceHostPath(t *testing.T) {
	t.Setenv("SANDBOX_RUNTIME_ID", "rt-1")
	t.Setenv("SANDBOX_TENANT_CODE", "tenant-a")
	t.Setenv("SANDBOX_CLONE_ID", "clone-1")
	t.Setenv("SANDBOX_CLONE_KEY", "clone-key")
	t.Setenv("SANDBOX_WORKSPACE", "/workspace")
	t.Setenv("SANDBOX_WORKSPACE_HOST_PATH", "/host/workspace")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.WorkspaceHostRoot != "/host/workspace" {
		t.Fatalf("WorkspaceHostRoot = %s", cfg.WorkspaceHostRoot)
	}
	if cfg.WorkspaceHost != "/host/workspace/tenant-a/clone-key/clone-1/work" {
		t.Fatalf("WorkspaceHost = %s", cfg.WorkspaceHost)
	}
}

func TestLoadFromEnv_SanitizesWorkspaceSegments(t *testing.T) {
	t.Setenv("SANDBOX_RUNTIME_ID", "rt-1")
	t.Setenv("SANDBOX_TENANT_CODE", "tenant a/../prod")
	t.Setenv("SANDBOX_CLONE_ID", "clone-1")
	t.Setenv("SANDBOX_CLONE_KEY", "clone key/../../draft")
	t.Setenv("SANDBOX_WORKSPACE", "/workspace")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	want := filepath.Join("/workspace", "tenant_a_.._prod", "clone_key_.._.._draft", "clone-1", "work")
	if cfg.Workspace != want {
		t.Fatalf("Workspace = %s, want %s", cfg.Workspace, want)
	}
}

func TestLoadFromEnv_CloneWorkspaceRemainsUniqueWhenCloneKeysCollide(t *testing.T) {
	t.Setenv("SANDBOX_RUNTIME_ID", "rt-1")
	t.Setenv("SANDBOX_TENANT_CODE", "tenant-a")
	t.Setenv("SANDBOX_WORKSPACE", "/workspace")

	t.Setenv("SANDBOX_CLONE_ID", "clone-1")
	t.Setenv("SANDBOX_CLONE_KEY", "clone/a")
	first, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("first LoadFromEnv() error = %v", err)
	}

	t.Setenv("SANDBOX_CLONE_ID", "clone-2")
	t.Setenv("SANDBOX_CLONE_KEY", "clone a")
	second, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("second LoadFromEnv() error = %v", err)
	}

	if first.Workspace == second.Workspace {
		t.Fatalf("expected unique workspaces, both resolved to %s", first.Workspace)
	}
}

func TestLoadFromEnv_MissingRequired(t *testing.T) {
	t.Setenv("SANDBOX_RUNTIME_ID", "")
	t.Setenv("SANDBOX_TENANT_CODE", "tenant-a")
	t.Setenv("SANDBOX_WORKSPACE", "/workspace")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("LoadFromEnv() should fail when required env missing")
	}
	if !strings.Contains(err.Error(), "SANDBOX_RUNTIME_ID") {
		t.Fatalf("unexpected error: %v", err)
	}
}
