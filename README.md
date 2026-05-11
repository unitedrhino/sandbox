# sandbox

`sandbox` 是联犀平台的 AI Agent 安全执行面服务（原 `claw`），为 AI Agent 提供隔离的任务执行环境。

## 功能概述

| 能力 | 说明 |
|------|------|
| **任务执行** | Python / Node / Shell 脚本隔离运行，支持 start / stop / status / exec |
| **资源限制** | cgroup v2 优先，Docker 环境下自动降级为 prlimit；容器级 2核/3GB/512进程兜底 |
| **网络隔离** | `CLONE_NEWNET` + veth + iptables OUTPUT DROP + SOCKS5 代理白名单 |
| **文件隔离** | clone 级 workspace 按 `<tenant>/<clone>/<cloneId>/work` 自动隔离 |
| **SSE 事件流** | `text_delta`、`tool_call_start`、`tool_call_output`、`done` 等生命周期事件 |
| **Skill 运行时** | 三级信任模型：builtin / shared / mapped，支持版本切换与 guard scan |

## 架构设计

### 服务拓扑

```
User(前端/设备/业务调用)
    → 控制面（Agent/Clone/Session/Skill/Memory）
    → Docker Engine HTTP API
    → sandbox 容器
    → Workspace(宿主持久目录) + Skills(/opt/skills/*)
```

控制面保留在调用方（通过 Docker Engine API + HTTP/SSE 调用 sandbox）：
- Agent / Clone / Session 主数据模型
- Skill 元数据、知识加载、Memory 编排
- sandbox 容器生命周期管理（创建 / 启动 / 停止 / 探活）

`sandbox` 作为独立执行面：
- 单 clone 的 tool dispatch 与执行
- 脚本运行与 SSE 事件输出
- 资源限制与安全隔离落地

### 容器生命周期

1. Session 开始时，控制面通过 Docker Engine API 创建/启动容器
2. 等待 `GET /readyz` 探活通过（最多 20s）
3. Skill 执行通过 HTTP/SSE 发送到 sandbox 容器
4. Session 结束时，控制面停止并（可选）销毁容器

### 通信协议

| 接口 | 方法 | 用途 |
|------|------|------|
| `GET /readyz` | GET | 探活，确认 sandbox 已就绪 |
| `POST /runtime/start` | POST | 初始化 runtime 执行上下文 |
| `POST /runtime/turn` | POST | 投递一轮用户输入或 tool result |
| `GET /runtime/stream?sessionID=...` | GET | SSE 拉取执行事件流 |
| `POST /runtime/stop` | POST | 主动结束 runtime |
| `GET /runtime/status` | GET | 查看 runtime 当前状态 |

SSE 事件类型：`runtime_started`、`text_delta`、`tool_call_start`、`tool_call_output`、`tool_call_end`、`runtime_error`、`done`

### Workspace 挂载规则

**Workspace 路径规则：**
- 宿主机：`<WorkspaceRoot>/<agentID>/`
- 容器内：`/workspace`

**强约束：** 容器宿主侧只能 bind mount 到该 clone 的具体目录，不允许把整个 workspace 根目录挂进去（防止同容器内进程枚举并访问其他 clone 的工作目录）。

| 宿主路径 | 容器路径 | 说明 |
|----------|----------|------|
| `<WorkspaceRoot>/<agentID>/` | `/workspace` | clone 工作目录 |
| `<ControlRoot>/<agentID>/` | `/runtime/control` | 控制面通信目录 |
| `<MappedSkillRoot>/` | `/opt/skills/mapped` | mapped 技能资产（可选） |
| `<SharedSkillRoot>/` | `/opt/skills/shared` | shared 技能资产（可选） |
| `<CommonSkillRoot>/` | `/opt/skills-store/common` | common 技能资产（可选） |

## 资源限制

### Sandbox 内部限制（单任务）

| 资源 | 默认值 | 限制方式 | 说明 |
|------|--------|----------|------|
| CPU | 25%（0.25 核） | `cpu.max`（cgroup） | Docker 环境下 cgroup 不可写，依赖容器级 `NanoCPUs` 兜底 |
| 内存 | 256 MB | `memory.max`（cgroup）→ `prlimit --as`（降级） | cgroup 不可写时自动降级为 prlimit `--as` |
| 进程数 | 32 | `pids.max`（cgroup）→ `prlimit --nproc`（降级） | cgroup 不可写时自动降级为 prlimit `--nproc` |
| 单文件大小 | 100 MB | — | — |
| workspace 总大小 | 1 GB | — | — |
| 超时 | 300 秒 | — | — |

> **降级机制**：启动任务时检测 `/sys/fs/cgroup` 可写性。不可写（如 Docker cgroup v2）时，自动将命令包装为 `prlimit --as=<内存> --nproc=<进程数> -- <原命令>`。

### Docker 容器整体限制

| 资源 | 默认值 | 配置字段 | 说明 |
|------|--------|----------|------|
| 内存 | 3072 MB | `ContainerMemoryMB` | 硬限制，超限时 OOM |
| CPU | 200%（2 核） | `ContainerCPUQuota` | 百分比，200 = 2 核 |
| 进程数 | 512 | `ContainerPidsLimit` | 防容器内 fork bomb |

## 安全边界

### 网络隔离

sandbox 强隔离路径（需要 root + `CAP_NET_ADMIN`）：

```
sandbox main process          → 默认网络（可访问外部服务）
sandboxed task process tree   → 受限 netns（只能访问 SOCKS5 代理口）
                                → 以 runner UID/GID（10001）运行，无法修改 iptables
```

**iptables OUTPUT 策略：**

| 规则 | 动作 | 说明 |
|------|------|------|
| `-P OUTPUT DROP` | 默认丢弃 | 所有出站流量默认拒绝 |
| `-o lo -j ACCEPT` | 允许回环 | lo 接口通信不受限 |
| `-m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT` | 允许响应 | 已建立连接的返回流量 |
| `-d <BridgeIP> -p tcp --dport <ProxyPort> -j ACCEPT` | 允许代理 | 仅允许访问 SOCKS5 代理 |
| `-p udp -j DROP` | 丢弃 UDP | 显式禁用 UDP 出站 |

**IP 黑名单：**

```
IPv4: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8,
      169.254.0.0/16, 0.0.0.0/8, 224.0.0.0/4, 240.0.0.0/4
IPv6: ::1/128, fc00::/7, fe80::/10, ff00::/8
```

### 已验证的防护

| 攻击类型 | 防护机制 | 状态 |
|---------|---------|------|
| 未挂载宿主目录访问 | bind mount 最小化 | 已验证 |
| docker.sock 滥用 | 不挂载 docker.sock | 已验证 |
| 只读目录写入 | 只读 bind mount | 已验证 |
| 内存爆炸 | cgroup `memory.max` → prlimit `--as` | prlimit 降级生效 |
| Fork bomb | cgroup `pids.max` → prlimit `--nproc` | prlimit 降级生效 |
| 云元数据泄露 | `169.254.169.254` 黑名单 | 已验证 |
| 内网访问（强隔离路径） | SOCKS5 + IP 过滤 | 已验证 |
| UDP 出站绕过 | iptables OUTPUT DROP + 显式 UDP DROP | 已验证 |
| iptables 规则篡改 | 子进程降权到 runner UID（10001） | 已验证 |

### 漏洞修复记录

| 漏洞 | 等级 | 修复方式 |
|------|------|----------|
| `BuildSkillEnv` 向子进程注入长期 AK/SK 凭证 | 高危 | 预生成 1 小时 JWT，只注入 `UR_TOKEN` |
| 网络隔离时子进程保持 root，可 `iptables -F` 清空规则 | 高危 | `CLONE_NEWNET + Credential` 同时设置，fork 后 exec 前降权 |
| 原始 socket / UDP 绕过 OUTPUT DROP | 中危 | 显式添加 `-p udp -j DROP` |
| 子进程继承全部环境变量，可能泄露敏感信息 | 高危 | `buildSafeEnv()` 白名单模式 |
| 网络隔离默认关闭，需手动启用 | 高危 | `EnableSandboxNet` 默认 `true` |
| stdout/stderr 无大小限制导致 OOM | 高危 | 每流限制 10MB，`limitWriter` 截断 |
| 并发任务无限制导致资源耗尽 | 中危 | 并发任务上限 32 |

## 本机运行

```bash
go build -o sandbox .

CLAW_LISTEN_ADDR=:18080 \
CLAW_RUNTIME_ID=rt-1 \
CLAW_TENANT_CODE=t1 \
CLAW_CLONE_ID=c1 \
CLAW_CLONE_KEY=clone-a \
CLAW_WORKSPACE=/tmp/sandbox-workspace \
./sandbox
```

工作区口径：

- `CLAW_WORKSPACE` 表示 workspace 根目录，不再是最终任务目录
- sandbox 会自动派生 clone 级实际工作区：
  - `<workspace-root>/<tenantCode>/<cloneKey>/<cloneId>/work`
  - 若 `cloneKey` 不可用，则回退到 `<workspace-root>/<tenantCode>/<cloneId>/work`
- 开启 `CLAW_ENABLE_MOUNT_SANDBOX=true` 时，任务进程会把自己的 clone 工作区视为 `/workspace`
- 未开启 mount sandbox 时，只保证任务默认 cwd 落在 clone 工作区，不代表它看不到其他容器内路径
- 宿主机上的真实文件会落到 clone 子目录，而不是直接落到 workspace 根

## 最小接口

- `GET /healthz`
- `GET /readyz`
- `GET /runtime/status`
- `POST /runtime/tasks`
- `GET /runtime/tasks`
- `GET /runtime/tasks/{id}`
- `POST /runtime/tasks/{id}/cancel`
- `POST /runtime/skills/{name}/activate`
- `POST /runtime/skills/reload`
- `POST /runtime/start`
- `POST /runtime/exec`
- `POST /runtime/stop`
- `GET /runtime/stream`

## 本机 smoke

```bash
# 在仓库根目录
bash tests/smoke-http.sh
bash tests/smoke-sandbox.sh
bash tests/smoke-docker.sh
bash tests/smoke-compose.sh
bash tests/smoke-task.sh
bash tests/smoke-stream.sh
bash tests/smoke-skill-ur-api.sh
bash tests/smoke-skill-mapped.sh
bash tests/smoke-skill-mapped-blocked.sh
bash tests/smoke-skill-direct-path-blocked.sh
bash tests/smoke-skill-shared.sh
```

## Docker 运行

```bash
# 在仓库根目录
docker build -t sandbox:dev .
docker network create sandbox-runtime-net

docker run --rm -p 18080:8080 \
  --network sandbox-runtime-net \
  -e CLAW_RUNTIME_ID=rt-1 \
  -e CLAW_TENANT_CODE=t1 \
  -e CLAW_CLONE_ID=c1 \
  -e CLAW_CLONE_KEY=clone-a \
  -e CLAW_WORKSPACE=/workspace \
  -e CLAW_ENABLE_SANDBOX_NET=true \
  -v /tmp/sandbox-workspace:/workspace \
  sandbox:dev
```

上面这个例子里：

- 容器内 root workspace 是 `/workspace`
- `tenant=t1`、`clone=clone-a` 时，实际执行目录是：
  - `/workspace/t1/clone-a/c1/work`
- 任务在 sandbox 内写入 `/workspace/task.txt`
- 宿主机真实文件会落到：
  - `/tmp/sandbox-workspace/t1/clone-a/c1/work/task.txt`

推荐口径：
- sandbox 容器使用独立 Docker network，不再复用业务 network
- 不可信命令默认开启 `CLAW_ENABLE_SANDBOX_NET=true`
- 允许访问的目标继续通过现有 SOCKS5 代理白名单控制
- 若需要访问宿主暴露的业务入口，显式传：
  - `CLAW_SANDBOX_ALLOWED_CIDRS=<host-gateway>/32`
  - `CLAW_SANDBOX_ALLOWED_PORTS=<port1,port2>`
  - 再通过 `host.docker.internal:<port>` 访问允许的入口

## Docker Compose 运行

```bash
# 在仓库根目录
cp .env.example .env
mkdir -p .temp/sandbox-compose/{workspace,control,skills-state}
docker compose up -d --build
docker compose ps
```

Compose 入口特性：
- 使用独立 network：`${SANDBOX_DOCKER_NETWORK:-sandbox_runtime_net}`
- 不复用业务 network
- 工作目录、控制目录、skills-state 统一落在仓库 `.temp/`
- 外部 skill 挂载点：
  - `${SANDBOX_MAPPED_SKILLS_DIR}` -> `/opt/skills/mapped`
  - `${SANDBOX_SHARED_SKILLS_DIR}` -> `/opt/skills/shared`
- workspace 挂载目录视为根目录；实际 clone 工作区自动落到：
  - `.temp/sandbox-compose/workspace/<tenantCode>/<cloneKey>/<cloneId>/work`
- 默认开启：
  - `CLAW_ENABLE_MOUNT_SANDBOX=true`
  - `CLAW_ENABLE_SANDBOX_NET=true`

若需要允许访问宿主暴露的业务入口，在 `.env` 中填写：

```env
CLAW_SANDBOX_ALLOWED_CIDRS=172.17.0.1/32
CLAW_SANDBOX_ALLOWED_PORTS=19091,7777
UR_BASE_URL=http://host.docker.internal:19091
```

可直接运行 compose 启停 smoke：

```bash
# 在仓库根目录
bash tests/smoke-compose.sh
```

它会实际验证：
- `docker compose up -d --build`
- `GET /healthz`
- `POST /runtime/start`
- 容器只挂在指定独立 network
- `docker compose down -v --remove-orphans`
- 独立 network 随 compose 停止被清理

## 当前已验证的重点

1. 普通命令执行可用
2. `stop` 可取消长任务
3. `stream` 能输出生命周期事件
4. 开启 `CLAW_ENABLE_SANDBOX_NET=true` 后，private direct connect 会失败
5. 任务进程可写 `/workspace`
   - 开启 mount sandbox 时，clone 之间默认映射到不同的 `/workspace`
   - 未开启 mount sandbox 时，只隔离默认工作目录，不等于完整文件可见性隔离
6. `OPENAI_API_KEY` / `OPENAI_MODEL` 这类大模型 env 会自动注入到任务进程
7. 开启 `CLAW_ENABLE_MOUNT_SANDBOX=true` 后，任务进程默认看不到 `/runtime/control`
8. Docker 端到端 smoke 已验证：
   - 任务工作目录可写
   - 运行镜像已预装 `curl`，任务可直接执行 `curl --version`
   - `runtime/exec` 下的 `curl` 可通过 SOCKS5 访问 allowlist 目标，并返回 `proxy-ok`
   - 清掉代理 env 后，同一 allowlist 目标会直连失败
   - 大模型 env 注入可用
   - 控制目录在 mount sandbox 下不可见
   - 攻击后 `healthz` 仍可用
9. task 模型已具备最小能力：
   - 可提交 task
   - 可列出 task
   - 可查询 task 状态
   - 可查询 task 执行结果
   - 可取消运行中的 task
10. runtime 已开始区分自有与映射 skills：
    - builtin skill 返回 `source=builtin`、`trustLevel=builtin`
    - shared skill 返回 `source=shared`、`trustLevel=trusted`
    - mapped skill 返回 `source=mapped`、`trustLevel=community`
    - 已验证 `/runtime/skills`、`/runtime/skills/{name}` 可查询
    - 已验证 mapped skill 样板可通过 `claw-skill demo-skill run` 执行
    - 已验证 shared skill 样板可通过 `claw-skill team-skill run` 执行
11. mapped skill 升级 / reload 已可用：
    - 支持版本目录 + `current` 指针
    - `POST /runtime/skills/{name}/activate`
    - `POST /runtime/skills/reload`
    - 已验证 `demo-skill` 从 `v1` 切到 `v2` 后立刻生效
12. shared skill 版本切换已可用：
    - 版本目录 + `current` 指针
    - `POST /runtime/skills/{name}/activate`
    - 已验证 `team-skill` 从 `v1` 切到 `v2` 后立刻生效
13. task / SSE 契约已开始稳定暴露 skill 元数据：
    - task 结果包含 `skillSource`、`skillTrustLevel`
    - stream 事件包含 `skillSource`、`skillTrustLevel`
    - 已验证 builtin skill 的事件流会返回 `builtin/builtin`
14. mapped skill 已接入最小 guard / scan：
    - catalog 会返回 `enabled`、`scanVerdict`、`blockedReason`
    - 当前已实现结构检查 + 少量危险模式扫描
    - dangerous mapped skill 会被保留在 catalog 中，但不能进入执行链
    - 已验证 blocked mapped skill 端到端会被 runtime 拒绝执行
15. external skill 的 direct path 执行已被运行时拦截：
    - 直接执行 `/opt/skills/shared/...` 或 `/opt/skills/mapped/...` 会被拒绝
    - 已验证通过 `/runtime/exec` 下发 shell 字符串直链时返回 400
16. 严格网络隔离 smoke 已验证：
    - `network_matrix.sh` 确认容器仅挂载独立 sandbox network，不落到默认 `bridge`
    - `smoke-docker.sh` 在独立 network 下可正常执行任务、写工作区、隐藏控制目录
    - `smoke-skill-ur-api.sh` 在独立 network 下仍可通过宿主暴露入口完成 API 调用
17. 完整安全 smoke 分项已验证：
    - `test_demo.sh`
    - `network_matrix.sh`
    - `sandbox_go_probe.sh`
    - `sandbox_go_network_strong_probe.sh`
    - `sandbox_go_private_service_bypass_probe.sh`
    - `sandbox_go_process_tree_inheritance_probe.sh`
    - `same_container_control_plane_risk_probe.sh`
    - `same_container_control_plane_mitigated_probe.sh`

当前结论：
- sandbox 容器级网络边界已收窄到独立 network
- 任务级强隔离（`CLAW_ENABLE_SANDBOX_NET=true`）下，直连私网目标失败，代理端口仍可用
- clone 级 workspace 已按 `<tenant>/<clone>/<cloneId>/work` 自动隔离，文件处理任务不会再直接共用 workspace 根目录
- 若关闭 mount sandbox，任务仍可能主动访问容器内其他可见路径；完整文件视野隔离仍依赖 mount sandbox
- 同容器模型下如果主进程与任务共享用户/控制目录，控制平面存在真实风险
- 通过"不同 UID/GID + 只读控制目录 + 可写任务工作区"的最小加固后，该风险可被有效压住

## Skill 升级模型

- `builtin`
  - 跟随镜像版本升级
  - 不支持运行时切换版本
- `shared`
  - 支持 `versions/<version>/...`
  - 支持 `current` 指针
  - 支持 `POST /runtime/skills/{name}/activate`
  - `activate` 状态写入控制目录覆盖层，不依赖 external skill 根目录可写
- `mapped`
  - 支持 `versions/<version>/...`
  - 支持 `current` 指针
  - 支持 `POST /runtime/skills/{name}/activate`
  - `activate` 状态写入控制目录覆盖层，不依赖 external skill 根目录可写
  - 仍保留 `POST /runtime/skills/reload` 重新扫描 actions / 元数据

## 相关文档

| 文档 | 路径 | 说明 |
|------|------|------|
| 运行时与安全隔离 | `docs/design/运行时与安全隔离.md` | 完整架构设计、配置说明、安全边界总结 |
| 沙盒方案内存对比 | `docs/design/沙盒方案内存对比.md` | 七方沙盒方案内存占用横向对比 |
| 沙箱网络代理 | `docs/design/沙箱网络代理.md` | veth + SOCKS5 网络隔离方案 |
| Skill 沙箱安全执行 | `docs/design/Skill沙箱安全执行.md` | 运行型 Skill 沙箱安全执行，攻击向量覆盖 |
| Workspace 版本管理 | `docs/design/Workspace版本管理.md` | Snapshot + Diff 轻量级版本管理 |
| RustFS 磁盘映射 | `docs/design/RustFS磁盘映射.md` | Workspace 存储方案 |
| 资源评估与交付基线 | `docs/design/资源评估与交付基线.md` | 资源消耗实测、镜像方案、交付验收标准 |
