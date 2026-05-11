# claw security smoke demo

## Purpose

This demo validates the current `claw` container isolation assumptions:

- workspace-mounted files are readable
- host files outside the mount are not visible
- `docker.sock` is not exposed
- `docker` command cannot control the host
- wrapping dangerous behavior in a script does not bypass the missing mount / missing docker socket
- container internal rootfs is still visible unless a stronger filesystem sandbox is added

## Run

```bash
cd backend/claw/tests/security-smoke
chmod +x run_demo.sh test_demo.sh
./run_demo.sh
./test_demo.sh
./sandbox_go_probe.sh
./sandbox_go_network_strong_probe.sh
```

完整走 Docker 内自测可使用：

```bash
cd backend/claw/tests/security-smoke
chmod +x run_in_docker.sh run_in_docker_suite.sh
./run_in_docker.sh
```

这条路径会：

- 构建 `Dockerfile`
- 使用 Alpine 测试 runner 镜像
- 以 `--privileged` 方式启动一个 DinD 测试容器
- 在测试容器内启动 `dockerd`
- 将宿主已有的 `claw-runtime:node24-python` 导出为 tar 并在容器内 `docker load`
- 将宿主 Go toolchain 与模块缓存只读挂入容器，避免 probe 再次走外网拉依赖
- 再由测试容器完整执行：
  - `./test_demo.sh`
  - `./network_matrix.sh`
  - `./sandbox_go_probe.sh`
  - `./sandbox_go_network_strong_probe.sh`
  - `./sandbox_go_private_service_bypass_probe.sh`
  - `./sandbox_go_process_tree_inheritance_probe.sh`
  - `./same_container_control_plane_risk_probe.sh`
  - `./same_container_control_plane_mitigated_probe.sh`

说明：

- `run_in_docker.sh` 不依赖宿主机 `docker.sock`
- 它依赖 DinD，因此测试容器必须以 `--privileged` 运行
- 由于当前仓库 `go.work` / `go.mod` 版本要求较高，Docker 内 probe 默认复用宿主 Go toolchain 与模块缓存
- `sandbox_go_probe.sh` 和 `sandbox_go_network_strong_probe.sh` 已改为相对路径定位仓库，可在容器内工作
- 在 DinD 环境下，`host_published_internal_service_reachable` 预期为 `false`

## Expected result

- Host files outside `/workspace` should be blocked
- `docker` usage should fail
- `/etc/passwd` inside the container will still be readable
- `/tmp` inside the container will still be writable

This means the current container model protects the host mount boundary, but does **not** yet provide a strict “only `/workspace` is visible” root filesystem sandbox.

The `sandbox_go_probe.sh` helper validates the current host/runtime package capability:

- whether `cgroup v2` is available
- whether the process has root/CAP requirements for the stronger network sandbox
- whether `NewSandboxNetwork` can create a real isolated network or only falls back

The `sandbox_go_network_strong_probe.sh` helper is a root-only integration probe
for the stronger `netns + veth + socks5` path. It now compiles the probe binary
as the current user and executes that binary via `sudo`, which avoids the
previous `sudo go run` hang. A passing result means the host can actually bring
up the strong `sandbox-go` network path under root.

When running through `run_in_docker.sh`, the same strong probe is executed as
root inside the DinD test container, and the runtime image is preloaded into
the inner `dockerd` instead of being re-pulled from Docker Hub.

The `sandbox_go_private_service_bypass_probe.sh` helper tests a more important
distinction:

- a direct raw TCP connect from the sandbox netns to a private host-side service
- a SOCKS5 connect request to that same private target

Current result:

- direct raw TCP connect is blocked
- proxy port remains reachable
- SOCKS5 path is blocked with reply code `5`

This means the current `sandbox-go` strong path now enforces proxy-only egress
for private targets on the sandbox netns.

The `sandbox_go_process_tree_inheritance_probe.sh` helper verifies that:

- parent / child / grandchild processes stay in the same sandbox netns
- all of them keep `directConnectOk=false`
- all of them still retain `proxyPortOk=true`

This proves the process-tree inheritance behavior needed for a same-container
`claw main process` + `sandboxed task process tree` model.

The `same_container_control_plane_risk_probe.sh` helper measures the opposite
side of the same-container model:

- a same-user task process tries to `SIGKILL` a simulated `claw main process`
- that same-user task process tries to delete a shared writable control file

Current result:

- `sameUserKillSucceeded=true`
- `sameUserDeleteSucceeded=true`

So if `claw main process` and sandboxed task still share the same user and the
same writable control directory, the task can damage the control plane.

The `same_container_control_plane_mitigated_probe.sh` helper validates the
minimum mitigation for a same-container model:

- `claw main process` runs under a different UID
- control files live in a readonly control directory
- task code still keeps write access to its own task workspace

Current result:

- `crossUserKillBlocked=true`
- `readonlyControlDeleteBlocked=true`
- `taskWorkspaceWriteSucceeded=true`

So the minimum hardening pattern is viable: different UID/GID for the control
plane, readonly control files, writable task workspace.

## Clone workspace semantics

`CLAW_WORKSPACE` is now treated as a workspace root, not a single clone's final
working directory.

- Effective per-clone path:
  - `<workspace-root>/<tenantCode>/<cloneKey>/<cloneId>/work`
- If `cloneKey` is unavailable, the path falls back to
  `<workspace-root>/<tenantCode>/<cloneId>/work`.
- Inside the mount sandbox, the task still sees its own directory as
  `/workspace`.

This means:

- multiple clones can share one mounted workspace root without writing into the
  same top-level directory
- document-processing tasks such as Python-based Word handling, extraction, and
  temp file generation now land in clone-specific subdirectories by default
- if mount sandbox is disabled, the task process still starts in its own clone
  directory, but it may still actively access sibling clone paths and other
  container-visible locations; the strict "only its own `/workspace` is
  visible" boundary still depends on mount sandbox
