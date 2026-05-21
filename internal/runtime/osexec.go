package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"gitee.com/unitedrhino/sandbox/internal/config"
	"gitee.com/unitedrhino/sandbox/internal/proxy"
)

type OSExecutor struct {
	engine *RuntimeEngine
}

func NewOSExecutor(opts config.ExecOptions) *OSExecutor {
	return &OSExecutor{engine: NewRuntimeEngine(opts)}
}

func (e *OSExecutor) Execute(ctx context.Context, req ExecRequest) (ExecResult, error) {
	return e.engine.Execute(ctx, req)
}

func (e *OSExecutor) Describe(req ExecRequest) (ExecutionMetadata, error) {
	return e.engine.Describe(req)
}

func (e *OSExecutor) ListSkills() []SkillInfo {
	return e.engine.ListSkills()
}

func (e *OSExecutor) GetSkill(name string) (SkillInfo, bool) {
	return e.engine.GetSkill(name)
}

func (e *OSExecutor) ReloadSkills() error {
	return e.engine.ReloadSkills()
}

func (e *OSExecutor) ActivateSkill(name, version string) (SkillInfo, error) {
	return e.engine.ActivateSkill(name, version)
}

type backendFeatures struct {
	mountSandbox bool
	network      bool
	resources    bool
}

var sandboxInstanceSeq atomic.Uint32

func executeCommand(ctx context.Context, spec ExecutionSpec, opts config.ExecOptions, features backendFeatures) (ExecResult, error) {
	if len(spec.Command) == 0 {
		return ExecResult{}, errors.New("command is required")
	}
	if err := validateDirectExternalSkillAccess(spec, opts); err != nil {
		return ExecResult{}, err
	}

	execCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	command := spec.Command
	var startGate *commandStartGate
	if os.Geteuid() == 0 {
		if features.mountSandbox {
			command = wrapWithMountSandbox(spec.Workspace, opts.ControlDir, opts.RunnerUID, opts.RunnerGID, command)
		}
		if features.network {
			var err error
			startGate, err = newCommandStartGate()
			if err != nil {
				return ExecResult{}, err
			}
			command = startGate.Wrap(command)
		}
	}
	if startGate != nil {
		defer func() { _ = startGate.Close() }()
	}

	cmd := exec.CommandContext(execCtx, command[0], command[1:]...)
	cmd.Dir = spec.Workspace
	// Build a minimal safe environment: only known-safe variables plus
	// SANDBOX_* runtime vars. Do not inherit the full os.Environ() which
	// may contain sensitive data injected by the container orchestrator.
	cmd.Env = buildSafeEnv(os.Environ(), opts.RunnerUID)
	if startGate != nil {
		startGate.Attach(cmd)
	}
	if opts.ControlDir != "" {
		cmd.Env = append(cmd.Env, "SANDBOX_SKILL_STATE_DIR=/runtime/skills-state")
	}
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var netProxy *proxy.NetworkProxy
	proxyPort := opts.SandboxProxyPort
	if features.network && os.Geteuid() == 0 {
		cfg := proxy.DefaultNetworkConfig()
		instanceID := int(sandboxInstanceSeq.Add(1))
		cfg.BridgeIP, cfg.SandboxIP = proxy.GenerateUniqueIPs(instanceID)
		cfg.VethHost = fmt.Sprintf("vethh%x", instanceID)
		cfg.VethSandbox = fmt.Sprintf("veths%x", instanceID)
		if opts.SandboxProxyPort != 0 {
			cfg.ProxyPort = opts.SandboxProxyPort
		}
		proxyPort = cfg.ProxyPort
		netProxy = proxy.NewNetworkProxy(cfg)
		if len(opts.AllowedInternalTargets) > 0 {
			filter, err := buildInternalWhitelistFilter(opts.AllowedInternalTargets)
			if err != nil {
				return ExecResult{}, err
			}
			netProxy.SetIPFilter(filter)
		} else if len(opts.AllowedCIDRs) > 0 {
			filter, err := proxy.NewIPFilterWithConfig(proxy.IPFilterConfig{
				Mode:         "whitelist",
				AllowedCIDRs: opts.AllowedCIDRs,
				AllowedPorts: opts.AllowedPorts,
			})
			if err != nil {
				return ExecResult{}, err
			}
			netProxy.SetIPFilter(filter)
		} else if len(opts.BlockedCIDRs) > 0 {
			if err := netProxy.AddBlockedCIDRs(opts.BlockedCIDRs); err != nil {
				return ExecResult{}, err
			}
		}
		cmd.Env = append(cmd.Env, netProxy.GetProxyEnv()...)
	}

	sysProcAttr := &syscall.SysProcAttr{Setpgid: true}
	if features.network && os.Geteuid() == 0 {
		sysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
	}
	// Credential is applied after fork but before exec. CLONE_NEWNET already
	// created the new netns during fork (while the child still has root caps),
	// so the child can safely drop privileges before executing the command.
	// This prevents the sandboxed process from modifying iptables rules.
	if os.Geteuid() == 0 && !features.mountSandbox && (opts.RunnerUID != 0 || opts.RunnerGID != 0) {
		sysProcAttr.Credential = &syscall.Credential{
			Uid: opts.RunnerUID,
			Gid: opts.RunnerGID,
		}
	}
	cmd.SysProcAttr = sysProcAttr

	const maxOutputBytes = 10 * 1024 * 1024 // 10MB per stream
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, limit: maxOutputBytes}
	cmd.Stderr = &limitWriter{w: &stderr, limit: maxOutputBytes}

	// 如果 cgroup 不可写，降级为 prlimit 命令包装（需在 cmd.Start() 之前）
	if features.resources && !isCgroupWritable() {
		log.Printf("[INFO] cgroup not writable, fallback to prlimit")
		if err := WrapCommandWithPrlimit(cmd, ResourceLimits{
			CPUQuota:     opts.CPUQuota,
			MemoryMB:     opts.MemoryLimitMB,
			MaxProcesses: opts.MaxProcesses,
		}); err != nil {
			log.Printf("[WARN] WrapCommandWithPrlimit failed: %v", err)
		}
	}

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	if startGate != nil {
		if err := startGate.AfterStart(); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return ExecResult{}, err
		}
	}

	var networkCleanup func()
	if netProxy != nil {
		if err := netProxy.Setup(cmd.Process.Pid); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return ExecResult{}, err
		}
		go func() {
			_ = netProxy.Start(execCtx)
		}()
		if err := waitForTCPReady(execCtx, net.JoinHostPort(netProxy.GetBridgeIP(), strconv.Itoa(proxyPort))); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return ExecResult{}, err
		}
		if err := startGate.Release(); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return ExecResult{}, err
		}
		networkCleanup = func() {
			_ = netProxy.Stop()
			_ = netProxy.Cleanup()
		}
	} else if startGate != nil {
		if err := startGate.Release(); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return ExecResult{}, err
		}
	}

	var resourceCleanup func()
	if features.resources && isCgroupWritable() {
		limiter := NewResourceLimiter(ResourceLimits{
			CPUQuota:     opts.CPUQuota,
			MemoryMB:     opts.MemoryLimitMB,
			MaxProcesses: opts.MaxProcesses,
		})
		cleanup, err := limiter.Apply(cmd.Process.Pid)
		if err == nil {
			resourceCleanup = cleanup
		} else {
			log.Printf("[WARN] resource limiter apply failed for pid %d: %v", cmd.Process.Pid, err)
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	go func() {
		<-execCtx.Done()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	err := <-done
	endedAt := time.Now()
	if resourceCleanup != nil {
		resourceCleanup()
	}
	if networkCleanup != nil {
		networkCleanup()
	}

	if execCtx.Err() != nil {
		return ExecResult{
			ExitCode:  124,
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			StartedAt: startedAt,
			EndedAt:   endedAt,
		}, execCtx.Err()
	}

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		} else {
			return ExecResult{}, err
		}
	}

	return ExecResult{
		ExitCode:  exitCode,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}, nil
}

// buildSafeEnv creates a minimal safe environment for sandboxed processes.
// It only includes known-safe system variables plus SANDBOX_* runtime variables.
// Sensitive container-level variables (DB credentials, API keys, etc.) are excluded.
func buildSafeEnv(fullEnv []string, runnerUID uint32) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/home/runner",
		"TMPDIR=/tmp",
		"USER=runner",
		"LOGNAME=runner",
		"SHELL=/bin/sh",
	}
	for _, e := range fullEnv {
		if strings.HasPrefix(e, "SANDBOX_") {
			env = append(env, e)
		}
		// Preserve locale settings if present
		if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_") {
			env = append(env, e)
		}
		// Preserve timezone
		if strings.HasPrefix(e, "TZ=") {
			env = append(env, e)
		}
	}
	return env
}

// limitWriter wraps an io.Writer with a maximum size limit.
type limitWriter struct {
	w       io.Writer
	limit   int64
	written int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.written >= lw.limit {
		return 0, fmt.Errorf("output exceeds maximum size of %d bytes", lw.limit)
	}
	remaining := lw.limit - lw.written
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += int64(n)
	return n, err
}

func wrapWithMountSandbox(workspace, controlDir string, uid, gid uint32, command []string) []string {
	exportLines := []string{
		"export PATH='/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'",
		"export HOME='/workspace'",
		"export TMPDIR='/tmp'",
	}
	if controlDir != "" {
		exportLines = append(exportLines, "export SANDBOX_SKILL_STATE_DIR='/runtime/skills-state'")
	}

	script := strings.Join([]string{
		"set -e",
		"ROOTFS=$(mktemp -d /root/sandbox-rootfs-XXXXXX)",
		"chmod 0755 \"$ROOTFS\"",
		"cleanup() { umount -l \"$ROOTFS/workspace\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/bin\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/usr\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/lib\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/lib64\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/etc\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/dev\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/proc\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/opt\" >/dev/null 2>&1 || true; umount -l \"$ROOTFS/runtime/skills-state\" >/dev/null 2>&1 || true; rm -rf \"$ROOTFS\" >/dev/null 2>&1 || true; }",
		"trap cleanup EXIT",
		"mkdir -p \"$ROOTFS\"/bin \"$ROOTFS\"/usr \"$ROOTFS\"/lib \"$ROOTFS\"/lib64 \"$ROOTFS\"/etc \"$ROOTFS\"/dev \"$ROOTFS\"/proc \"$ROOTFS\"/tmp \"$ROOTFS\"/workspace \"$ROOTFS\"/opt \"$ROOTFS\"/runtime/skills-state",
		"mount --rbind /bin \"$ROOTFS/bin\"",
		"mount --rbind /usr \"$ROOTFS/usr\"",
		"test -e /lib && mount --rbind /lib \"$ROOTFS/lib\" || true",
		"test -e /lib64 && mount --rbind /lib64 \"$ROOTFS/lib64\" || true",
		"mount --rbind /etc \"$ROOTFS/etc\"",
		"mount --rbind /dev \"$ROOTFS/dev\"",
		"test -e /opt && mount --rbind /opt \"$ROOTFS/opt\" || true",
		fmt.Sprintf("test -d %q/skills-state && mount --bind %q/skills-state \"$ROOTFS/runtime/skills-state\" || true", controlDir, controlDir),
		"mount -t proc proc \"$ROOTFS/proc\"",
		fmt.Sprintf("mount --bind %q \"$ROOTFS/workspace\"", workspace),
		"mount -t tmpfs tmpfs \"$ROOTFS/tmp\"",
		"mount -o remount,bind,ro \"$ROOTFS/bin\"",
		"mount -o remount,bind,ro \"$ROOTFS/usr\"",
		"test -e \"$ROOTFS/lib\" && mount -o remount,bind,ro \"$ROOTFS/lib\" || true",
		"test -e \"$ROOTFS/lib64\" && mount -o remount,bind,ro \"$ROOTFS/lib64\" || true",
		"mount -o remount,bind,ro \"$ROOTFS/etc\"",
		"test -e \"$ROOTFS/opt\" && mount -o remount,bind,ro \"$ROOTFS/opt\" || true",
		"mountpoint -q \"$ROOTFS/runtime/skills-state\" && mount -o remount,bind,ro \"$ROOTFS/runtime/skills-state\" || true",
		fmt.Sprintf("cd /workspace && exec chroot --userspec=%d:%d \"$ROOTFS\" /bin/sh -c %s sh \"$@\"",
			uid, gid, shellSingleQuote(strings.Join(append(exportLines, `cd /workspace`, `exec "$@"`), "\n"))),
	}, "\n")

	args := []string{"unshare", "--mount", "--propagation", "private", "/bin/bash", "-lc", script, "sandbox-mount-sandbox"}
	args = append(args, command...)
	return args
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func validateDirectExternalSkillAccess(spec ExecutionSpec, opts config.ExecOptions) error {
	if spec.Skill != "" {
		return nil
	}
	sharedRoot := opts.SharedSkillRoot
	if strings.TrimSpace(sharedRoot) == "" {
		sharedRoot = DefaultSharedSkillRoot
	}
	mappedRoot := opts.MappedSkillRoot
	if strings.TrimSpace(mappedRoot) == "" {
		mappedRoot = DefaultMappedSkillRoot
	}
	joined := strings.Join(spec.Command, " ")
	if strings.Contains(joined, sharedRoot+"/") || strings.Contains(joined, mappedRoot+"/") {
		return fmt.Errorf("direct external skill path execution is blocked; use sandbox-skill")
	}
	for _, arg := range spec.Command {
		if strings.HasPrefix(arg, sharedRoot+"/") || strings.HasPrefix(arg, mappedRoot+"/") {
			return fmt.Errorf("direct external skill path execution is blocked; use sandbox-skill")
		}
	}
	return nil
}

func buildInternalWhitelistFilter(targets []string) (*proxy.IPFilter, error) {
	rules := make([]proxy.AllowRule, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		host, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return nil, fmt.Errorf("invalid SANDBOX_ALLOWED_INTERNAL_TARGETS entry %q: %w", target, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SANDBOX_ALLOWED_INTERNAL_TARGETS port %q: %w", target, err)
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed internal target %q: %w", target, err)
		}
		for _, ip := range ips {
			if ip == nil {
				continue
			}
			rules = append(rules, proxy.AllowRule{IP: ip, Port: port})
		}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("no valid SANDBOX_ALLOWED_INTERNAL_TARGETS resolved")
	}
	return proxy.NewWhitelistFilterWithPorts(rules)
}
