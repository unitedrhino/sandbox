package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type ResourceLimits struct {
	CPUQuota     int64
	MemoryMB     int64
	MaxProcesses int64
}

type ResourceLimiter struct {
	limits ResourceLimits
}

func NewResourceLimiter(limits ResourceLimits) *ResourceLimiter {
	if limits.CPUQuota == 0 {
		limits.CPUQuota = 25
	}
	if limits.MemoryMB == 0 {
		limits.MemoryMB = 256
	}
	if limits.MaxProcesses == 0 {
		limits.MaxProcesses = 32
	}
	return &ResourceLimiter{limits: limits}
}

// isCgroupWritable 检查容器内是否可以创建子 cgroup
func isCgroupWritable() bool {
	cgroupRoot := "/sys/fs/cgroup"
	testPath := filepath.Join(cgroupRoot, fmt.Sprintf("test_write_%d", os.Getpid()))
	if err := os.Mkdir(testPath, 0o755); err != nil {
		return false
	}
	_ = os.Remove(testPath)
	return true
}

// WrapCommandWithPrlimit 使用系统 prlimit 命令包装执行命令
// 当 cgroup 不可用时作为降级方案。
//
// 注意：
// 1. --nproc 被有意跳过。RLIMIT_NPROC 是 UID 级别的限制，在 Docker 默认
//    配置（无 userns-remap）下，多个容器中的同一个 UID 会共享 nproc 配额。
//    Docker 的 PidsLimit 提供了按容器的进程数保护，足以替代 prlimit --nproc。
// 2. --as（虚拟地址空间）不限制，因为 Go 程序启动需要较大的虚拟地址空间，
//    限制后会导致 runtime 报错 "failed to reserve page summary memory"。
//    Docker 容器已通过 HostConfig.Memory 做了硬限制。
func WrapCommandWithPrlimit(cmd *exec.Cmd, limits ResourceLimits) error {
	// Docker 容器本身已有 Memory/CPU/Pids 限制，prlimit 降级方案不再额外限制资源。
	// 这里仅保留命令包装结构，以便后续如需增加其他 prlimit 限制可在此扩展。
	_ = limits

	prlimitPath, err := exec.LookPath("prlimit")
	if err != nil {
		return fmt.Errorf("find prlimit: %w", err)
	}

	args := []string{}
	args = append(args, "--")
	args = append(args, cmd.Path)
	args = append(args, cmd.Args[1:]...)

	cmd.Path = prlimitPath
	cmd.Args = append([]string{"prlimit"}, args...)

	return nil
}

func (r *ResourceLimiter) Apply(pid int) (func(), error) {
	if os.Geteuid() != 0 {
		return nil, nil
	}
	cgroupRoot := "/sys/fs/cgroup"
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return nil, nil
	}

	cgroupName := fmt.Sprintf("sandbox_%d_%d", os.Getpid(), pid)
	cgroupPath := filepath.Join(cgroupRoot, cgroupName)
	if err := os.Mkdir(cgroupPath, 0o755); err != nil {
		return nil, fmt.Errorf("create cgroup: %w", err)
	}

	cleanup := func() {
		_ = removeCgroup(cgroupPath)
	}

	if r.limits.CPUQuota > 0 {
		quota := r.limits.CPUQuota * 1000
		cpuMax := fmt.Sprintf("%d %d", quota, 100000)
		if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(cpuMax), 0o644); err != nil {
			cleanup()
			return nil, fmt.Errorf("set cpu.max: %w", err)
		}
	}

	if r.limits.MemoryMB > 0 {
		memoryBytes := r.limits.MemoryMB * 1024 * 1024
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.FormatInt(memoryBytes, 10)), 0o644); err != nil {
			cleanup()
			return nil, fmt.Errorf("set memory.max: %w", err)
		}
		_ = os.WriteFile(filepath.Join(cgroupPath, "memory.swap.max"), []byte("0"), 0o644)
	}

	if r.limits.MaxProcesses > 0 {
		_ = os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(strconv.FormatInt(r.limits.MaxProcesses, 10)), 0o644)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("add process to cgroup: %w", err)
	}
	return cleanup, nil
}

func removeCgroup(cgroupPath string) error {
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	data, err := os.ReadFile(procsPath)
	if err == nil {
		for _, pidStr := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if pidStr == "" {
				continue
			}
			pid, convErr := strconv.Atoi(pidStr)
			if convErr == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	return os.Remove(cgroupPath)
}
