# sandbox 安全执行方案

> 更新时间：2026-05-11

## 背景

AI 生成的代码不可信，可能包含 `rm -rf`、端口扫描、内网穿透等恶意行为。应用层过滤（如正则黑名单）可被轻易绕过。

**目标**：在不使用 Docker 额外容器的前提下，为每个代码执行任务提供内核级隔离：
- 文件系统隔离：AI 代码只能访问 workspace 目录
- 网络隔离：允许外网，禁止访问内网 IP
- 资源限制：防 fork bomb、内存爆炸、无限循环
- 进程隔离：任务进程树独立，不可见宿主机进程

---

## 整体架构

```
代码执行请求
       ↓
OSExecutor
       ↓
┌──────────────────────────────────────────┐
│            任务执行层                      │
│  ┌────────────────────────────────────┐  │
│  │  Linux namespace 隔离               │  │
│  │  ├─ mount ns：chroot 最小根文件系统  │  │
│  │  ├─ pid ns：独立进程树（Setpgid）    │  │
│  │  ├─ user ns：Credential 降权到 runner│  │
│  │  └─ net ns：独立网络栈（可选）        │  │
│  ├────────────────────────────────────┤  │
│  │  文件系统隔离                        │  │
│  │  ├─ unshare --mount + chroot        │  │
│  │  └─ rbind 挂载（workspace 可写，其余只读）│  │
│  ├────────────────────────────────────┤  │
│  │  资源限制（cgroup v2 优先，prlimit 降级）│  │
│  │  ├─ memory.max = 256MB              │  │
│  │  ├─ pids.max = 32                   │  │
│  │  └─ cpu.max = 25%                   │  │
│  ├────────────────────────────────────┤  │
│  │  环境变量白名单                      │  │
│  │  （只保留 PATH/HOME/CLAW_*/locale）  │  │
│  └────────────────────────────────────┘  │
└──────────────────────────────────────────┘
       ↓ 网络流量（veth pair）
┌──────────────────────────────────────────┐
│  NetworkProxy（宿主机用户态代理）          │
│  ├─ 解析目标 IP                           │
│  ├─ 内网 IP → 拒绝连接                    │
│  └─ 公网 IP → 透明转发                    │
└──────────────────────────────────────────┘
       ↓
     互联网
```

---

## 组件一：进程与文件系统隔离

### 核心实现

```go
// internal/runtime/osexec.go

func executeCommand(ctx context.Context, spec ExecutionSpec, opts config.ExecOptions, features backendFeatures) (ExecResult, error) {
    cmd := exec.CommandContext(execCtx, command[0], command[1:]...)
    cmd.Dir = spec.Workspace

    // 1. 构建最小安全环境（白名单模式）
    cmd.Env = buildSafeEnv(os.Environ(), opts.RunnerUID)

    // 2. 配置 syscall.SysProcAttr
    sysProcAttr := &syscall.SysProcAttr{Setpgid: true}

    // 3. 网络隔离：创建新 netns
    if features.network && os.Geteuid() == 0 {
        sysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
    }

    // 4. 权限降权：fork 后 exec 前降到 runner UID
    // CLONE_NEWNET 在 fork 时创建新 netns（子进程仍有 root caps）
    // Credential 在 exec 前降权，防止子进程修改 iptables
    if os.Geteuid() == 0 && !features.mountSandbox && (opts.RunnerUID != 0 || opts.RunnerGID != 0) {
        sysProcAttr.Credential = &syscall.Credential{
            Uid: opts.RunnerUID,    // 默认 10001
            Gid: opts.RunnerGID,    // 默认 10001
        }
    }
    cmd.SysProcAttr = sysProcAttr

    // 5. 启动并等待
    cmd.Start()
    cmd.Wait()
}
```

### 文件系统隔离（mount sandbox）

通过 `wrapWithMountSandbox` 构建最小 chroot 环境：

```go
// internal/runtime/osexec.go
func wrapWithMountSandbox(workspace, controlDir string, uid, gid uint32, command []string) []string {
    script := strings.Join([]string{
        "set -e",
        "ROOTFS=$(mktemp -d /root/claw-rootfs-XXXXXX)",
        "cleanup() { umount -l ... ; rm -rf \"$ROOTFS\" ; }",
        "trap cleanup EXIT",
        // 创建目录结构
        "mkdir -p \"$ROOTFS\"/{bin,usr,lib,lib64,etc,dev,proc,tmp,workspace,opt,runtime/skills-state}",
        // rbind 挂载系统目录（后续 remount 为只读）
        "mount --rbind /bin \"$ROOTFS/bin\"",
        "mount --rbind /usr \"$ROOTFS/usr\"",
        "mount --rbind /lib \"$ROOTFS/lib\" || true",
        "mount --rbind /lib64 \"$ROOTFS/lib64\" || true",
        "mount --rbind /etc \"$ROOTFS/etc\"",
        "mount --rbind /dev \"$ROOTFS/dev\"",
        "mount --rbind /opt \"$ROOTFS/opt\" || true",
        // 挂载 workspace（可写）
        "mount --bind \"" + workspace + "\" \"$ROOTFS/workspace\"",
        "mount -t proc proc \"$ROOTFS/proc\"",
        "mount -t tmpfs tmpfs \"$ROOTFS/tmp\"",
        // 将系统目录 remount 为只读
        "mount -o remount,bind,ro \"$ROOTFS/bin\"",
        "mount -o remount,bind,ro \"$ROOTFS/usr\"",
        "mount -o remount,bind,ro \"$ROOTFS/lib\" || true",
        "mount -o remount,bind,ro \"$ROOTFS/lib64\" || true",
        "mount -o remount,bind,ro \"$ROOTFS/etc\"",
        "mount -o remount,bind,ro \"$ROOTFS/opt\" || true",
        // chroot 执行
        "exec chroot --userspec=" + uid + ":" + gid + " \"$ROOTFS\" /bin/sh -c ...",
    }, "\n")

    return []string{"unshare", "--mount", "--propagation", "private", "/bin/bash", "-lc", script, "claw-mount-sandbox"}
}
```

**沙箱内文件系统视图：**

```
沙箱内 /               (tmpfs，通过 chroot)
├── workspace/         ← bind mount 到 workspace 目录（读写）
├── bin/               ← rbind mount 宿主机 /bin（只读）
├── usr/               ← rbind mount 宿主机 /usr（只读）
├── lib/               ← rbind mount 宿主机 /lib（只读）
├── lib64/             ← rbind mount 宿主机 /lib64（只读）
├── etc/               ← rbind mount 宿主机 /etc（只读）
├── dev/               ← rbind mount 宿主机 /dev（只读）
├── proc/              ← procfs（仅沙箱 PID）
├── tmp/               ← tmpfs（沙箱内临时文件）
└── opt/               ← rbind mount 宿主机 /opt（只读）

宿主机 /home、/root、/var 等 → 沙箱内不可见
```

**关键技术点：**

- `unshare --mount`：创建独立 mount namespace
- `chroot`：切换根目录到临时 rootfs
- `rbind` + `remount,ro`：递归绑定并设为只读
- `--userspec=uid:gid`：chroot 内以指定用户运行

### 环境变量白名单

```go
// internal/runtime/osexec.go
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
        // 只保留 CLAW_* 运行时变量
        if strings.HasPrefix(e, "CLAW_") {
            env = append(env, e)
        }
        // 保留 locale
        if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_") {
            env = append(env, e)
        }
        // 保留时区
        if strings.HasPrefix(e, "TZ=") {
            env = append(env, e)
        }
    }
    return env
}
```

**安全效果**：DB 密码、API Key 等容器编排层注入的敏感环境变量不会被任务进程继承。

---

## 组件二：资源限制

### cgroup v2 优先方案

```go
// internal/runtime/resource_limiter.go

func (r *ResourceLimiter) Apply(pid int) (func(), error) {
    cgroupPath := filepath.Join("/sys/fs/cgroup", fmt.Sprintf("claw_%d_%d", os.Getpid(), pid))
    os.Mkdir(cgroupPath, 0o755)

    // CPU 限制
    cpuMax := fmt.Sprintf("%d %d", r.limits.CPUQuota*1000, 100000)
    os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(cpuMax), 0o644)

    // 内存限制
    memoryBytes := r.limits.MemoryMB * 1024 * 1024
    os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.FormatInt(memoryBytes, 10)), 0o644)
    os.WriteFile(filepath.Join(cgroupPath, "memory.swap.max"), []byte("0"), 0o644)  // 禁止 swap

    // 进程数限制
    os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(strconv.FormatInt(r.limits.MaxProcesses, 10)), 0o644)

    // 将进程加入 cgroup
    os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644)

    return func() { removeCgroup(cgroupPath) }, nil
}
```

### prlimit 降级方案

Docker 环境下 `/sys/fs/cgroup` 为只读，cgroup 不可写，自动降级为 `prlimit`：

```go
// internal/runtime/resource_limiter.go

func isCgroupWritable() bool {
    testPath := filepath.Join("/sys/fs/cgroup", fmt.Sprintf("test_write_%d", os.Getpid()))
    if err := os.Mkdir(testPath, 0o755); err != nil {
        return false
    }
    _ = os.Remove(testPath)
    return true
}

func WrapCommandWithPrlimit(cmd *exec.Cmd, limits ResourceLimits) error {
    args := []string{}
    if limits.MemoryMB > 0 {
        args = append(args, fmt.Sprintf("--as=%d", limits.MemoryMB*1024*1024))
    }
    // --nproc 被有意跳过（RLIMIT_NPROC 是 UID 级，Docker 多容器共享 UID 会互相影响）
    if len(args) > 0 {
        args = append(args, "--", cmd.Path)
        args = append(args, cmd.Args[1:]...)
        cmd.Path = "prlimit"
        cmd.Args = append([]string{"prlimit"}, args...)
    }
    return nil
}
```

**降级后行为：**

| 资源 | cgroup 可用 | cgroup 只读（Docker）|
|------|-------------|---------------------|
| CPU | `cpu.max` 精确控制 | 依赖 Docker `NanoCPUs` |
| 内存 | `memory.max` 硬限制 | `prlimit --as` 限制虚拟地址空间 |
| 进程数 | `pids.max` | Docker `PidsLimit` 兜底 |

---

## 组件三：网络隔离

网络隔离由 `internal/proxy` 包实现，详见[沙箱网络代理.md](沙箱网络代理.md)。

核心流程：

```
1. CLONE_NEWNET 创建独立 netns
2. veth pair 连接 sandbox netns 与 host
3. sandbox netns 内 iptables OUTPUT DROP
4. 仅允许访问 host 侧 SOCKS5 代理端口
5. SOCKS5 代理解析目标 IP，内网拒绝、外网转发
```

---

## 组件四：输出限制

防止 stdout/stderr 无限写入导致 OOM：

```go
// internal/runtime/osexec.go
const maxOutputBytes = 10 * 1024 * 1024 // 10MB per stream

type limitWriter struct {
    w       io.Writer
    limit   int64
    written int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
    if lw.written >= lw.limit {
        return 0, fmt.Errorf("output exceeds maximum size")
    }
    remaining := lw.limit - lw.written
    if int64(len(p)) > remaining {
        p = p[:remaining]  // 截断超额输出
    }
    n, err := lw.w.Write(p)
    lw.written += int64(n)
    return n, err
}
```

---

## 安全性分析

### 攻击向量覆盖

| 攻击类型 | 防护层 | 是否覆盖 |
|---------|--------|---------|
| `rm -rf /` | mount namespace + chroot（workspace 外只读） | 是 |
| 读 /etc/passwd | chroot 内 /etc 为 rbind 只读，不影响宿主机 | 是 |
| 读宿主机敏感文件 | mount namespace 隔离，未挂载目录不可见 | 是 |
| fork bomb | cgroup `pids.max` → prlimit / Docker PidsLimit | 是 |
| 内存耗尽 | cgroup `memory.max` → prlimit `--as` | 是 |
| 无限 CPU | cgroup `cpu.max` → Docker NanoCPUs | 是（有降级损失） |
| 访问内网 API | NetworkProxy IP 过滤 + iptables | 是 |
| DNS rebinding 打内网 | SOCKS5 解析 DNS 后二次检查 IP | 是 |
| 连接 169.254（云元数据）| 内网 CIDR 黑名单覆盖 | 是 |
| UDP 绕过 OUTPUT DROP | iptables `-p udp -j DROP` | 是 |
| 原始 socket 绕过 | OUTPUT DROP 对所有出站生效 | 是 |
| 扫描宿主机端口 | 独立 netns，veth 只走代理 | 是 |
| 写宿主机文件 | mount namespace + chroot 隔离 | 是 |
| 查看宿主机进程 | pid namespace（Setpgid） | 是 |
| 环境变量泄露 | `buildSafeEnv` 白名单过滤 | 是 |
| 直接执行外部 skill 路径 | `validateDirectExternalSkillAccess` 拦截 | 是 |
| stdout/stderr OOM | `limitWriter` 10MB 截断 | 是 |

### 已知局限

1. **无任务级 seccomp**：当前未使用 seccomp BPF 过滤系统调用
2. **时间侧信道**：无防护（非高安全场景可接受）
3. **容器 rootfs 仍可读**：chroot 内可读取 `/etc/passwd` 等
4. **/tmp 共享**：同一容器内多个任务共享 /tmp（同一 clone 信任域内）

---

## 实施路线图

### Phase 1（已完成）：文件系统 + 资源隔离
- [x] `internal/runtime/osexec.go`：mount namespace + chroot + Credential 降权
- [x] `internal/runtime/resource_limiter.go`：cgroup v2 + prlimit 降级
- [x] `internal/runtime/osexec.go`：buildSafeEnv 环境变量白名单
- [x] 单元测试：`rm -rf /` 无法删除宿主机文件

### Phase 2（已完成）：网络隔离
- [x] `internal/proxy/network.go`：veth pair + netns + iptables 管理
- [x] `internal/proxy/socks5.go`：SOCKS5 代理
- [x] `internal/proxy/ip_filter.go`：IP 黑名单/白名单
- [x] 集成测试：沙箱内能访问 baidu.com，不能访问 192.168.x.x

### Phase 3（已完成）：集成与加固
- [x] `commandStartGate`：pipe 门控机制
- [x] `limitWriter`：输出大小限制
- [x] `validateDirectExternalSkillAccess`：直接路径执行拦截
- [x] 并发任务上限 32

---

## 性能预估

| 指标 | 值 |
|------|-----|
| 沙箱启动时间 | < 50ms（namespace fork，无镜像拉取）|
| 内存基础开销 | ~2MB/沙箱（namespace 本身几乎零开销）|
| 网络代理延迟 | < 1ms（用户态转发，本机回环）|
| CPU 隔离精度 | cgroup v2，10ms 周期；Docker 环境依赖容器级限制 |
| 对比 Docker | 快 10-100x 启动，开销接近原生进程 |

---

## 相关源码位置

```
internal/runtime/
├── osexec.go           # 核心执行引擎（CLONE_NEWNET + Credential + mount sandbox）
├── resource_limiter.go # cgroup v2 资源限制 + prlimit 降级
└── start_gate.go       # 网络启动门控

internal/proxy/
├── network.go          # NetworkProxy（veth + netns + iptables）
├── socks5.go           # Socks5Server
└── ip_filter.go        # IPFilter（黑名单/白名单）
```
