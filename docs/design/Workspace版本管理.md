# Sandbox Workspace 版本管理

## 背景与需求

Sandbox 中的 Workspace 在执行任务过程中会自更新（如修改 `code/main.py`、`agent.md`、`learned.md`），需要轻量级版本管理机制支持：

1. **版本追溯**：查看历史变更
2. **异常回滚**：更新导致异常时回退
3. **跨 Workspace 同步**：管理员可帮助其他 Workspace 回滚

方案选型对比：

| 方案 | 复杂度 | 依赖组件 | 版本回滚 | 适用场景 |
|------|--------|---------|---------|---------|
| Git LFS 完整方案 | 高 | Git Server + LFS Server + RustFS | 完整 | 大文件多、需要分支 |
| 纯 Git（无 LFS） | 中 | Git Server + RustFS | 完整 | 文件小、需要分支 |
| RustFS S3 版本控制 | 低 | RustFS | 基础 | Bucket 级别，无法细分 |
| **快照 + Diff** | **最低** | **RustFS** | **基础** | **轻量优先，Workspace 级隔离** |

**选择：快照 + Diff 方案**。理由：依赖最少（只需 RustFS）、Workspace 级别天然隔离、满足自更新和回滚需求、实现简单。

## 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                  Workspace 版本管理                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  本地 workspace/                                            │
│  ├── agent.md              # 行为说明书                     │
│  ├── learned.md            # 学习记录                       │
│  ├── code/                 # 处理代码                       │
│  │   └── main.py                                           │
│  └── skills/               # 技能脚本（只读，平台下发）      │
│                                                             │
│  ────────────────────────────────────────────────────────  │
│                                                             │
│  SnapshotManager（快照管理器）                               │
│  ├── CreateSnapshot()      # 创建快照                       │
│  ├── RestoreSnapshot()     # 恢复快照                       │
│  ├── ListSnapshots()       # 列出历史                       │
│  └── DeleteSnapshot()      # 删除快照                       │
│                                                             │
│  ────────────────────────────────────────────────────────  │
│                                                             │
│  RustFS 存储：                                               │
│  workspaces/{tenant}/{workspace}/                           │
│  ├── snapshots.json        # 元数据列表                     │
│  ├── snap_a1b2c3d4.tar.gz  # 快照文件（checksum 前8位命名） │
│  └── snap_e5f6g7h8.tar.gz  # 最新版本                       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Workspace 结构

```
workspace/
├── agent.md              # 行为说明书：职责、边界、决策规则
├── learned.md            # 学习记录：历史经验、参数优化、失败案例
├── code/                 # 处理代码（沙箱执行）
│   ├── main.py          # 主入口
│   ├── utils.py         # 工具函数
│   └── requirements.txt # 依赖
└── skills/               # 技能脚本（平台下发，只读，不纳入版本）
    ├── get-device-status/
    │   ├── SKILL.md
    │   └── scripts/run.sh
    └── send-alert/
        ├── SKILL.md
        └── scripts/run.sh
```

## 快照元数据

```json
{
  "snapshots": [
    {
      "id": "a1b2c3d4",
      "timestamp": "2026-03-26T10:30:00Z",
      "message": "初始版本",
      "fileCount": 5,
      "size": 12345
    },
    {
      "id": "e5f6g7h8",
      "timestamp": "2026-03-26T11:00:00Z",
      "message": "优化大鹏告警阈值逻辑",
      "fileCount": 6,
      "size": 15678
    }
  ],
  "current": "e5f6g7h8"
}
```

快照 ID 使用 SHA256 checksum 前 8 位：内容相同则 checksum 相同，自然去重，可校验完整性。

## 核心操作流程

### 创建快照

```
Workspace 执行任务
     ↓
修改 workspace 文件
  - code/main.py：新增边界处理
  - learned.md：记录本次经验
     ↓
SnapshotManager.CreateSnapshot("优化XXX")
     ↓
1. 打包 workspace → tar.gz（跳过 skills/）
2. 计算 checksum
3. 上传到 RustFS
4. 更新 snapshots.json
5. 清理旧快照（保留最近 N 个）
     ↓
下次执行使用新版本
```

### 异常回滚

```
Workspace 执行异常
     ↓
SnapshotManager.ListSnapshots()
     ↓
选择目标快照（通常是上一个）
     ↓
SnapshotManager.RestoreSnapshot(snapshotID)
     ↓
1. 从 RustFS 下载快照
2. 解压到 workspace
3. 更新 current 指针
     ↓
恢复正常执行
```

### 跨 Workspace 回滚（管理员）

```
管理员查看 Workspace 快照历史
  - 调用 API: GET /workspace/snapshots?tenant=X&workspace=Y
     ↓
选择目标快照
  - 调用 API: POST /workspace/rollback
    {tenant: X, workspace: Y, snapshotId: "xxx"}
     ↓
服务端执行 RestoreSnapshot
     ↓
Workspace 恢复正常
```

## RustFS 存储结构

```
Bucket: workspaces

目录结构：
├── tenant_001/
│   ├── workspace_user_123/
│   │   ├── snapshots.json
│   │   ├── snap_a1b2c3d4.tar.gz
│   │   └── snap_e5f6g7h8.tar.gz
│   └── workspace_device_456/
│       ├── snapshots.json
│       └── snap_x1y2z3a4.tar.gz
│
├── tenant_002/
│   └── workspace_user_789/
│       ├── snapshots.json
│       └── snap_b2c3d4e5.tar.gz
│
└── .policy              # 生命周期策略
```

### 生命周期策略

| 限制类型 | 配置值 | 说明 |
|---------|--------|------|
| 数量限制 | 10 个 | 每个 Workspace 最多保留快照数 |
| 时间限制 | 30 天 | 超过 30 天的快照自动清理 |
| 大小限制 | 100MB | 总大小超过时清理最旧快照 |

```go
const (
    MaxSnapshotsPerWorkspace = 10
    MaxSnapshotAge           = 30 * 24 * time.Hour
    MaxTotalSizePerWorkspace = 100 * 1024 * 1024 // 100MB
)
```

## 核心实现

### SnapshotManager 结构

```go
package workspace

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)

type SnapshotManager struct {
    rustfs       RustFSClient
    bucket       string
    tenantCode   string
    workspaceCode string
    maxSnapshots int
    maxAge       time.Duration
    mu           sync.Mutex
}

type SnapshotMeta struct {
    ID        string    `json:"id"`
    Timestamp time.Time `json:"timestamp"`
    Message   string    `json:"message"`
    FileCount int       `json:"fileCount"`
    Size      int64     `json:"size"`
}

type SnapshotList struct {
    Snapshots []SnapshotMeta `json:"snapshots"`
    Current   string         `json:"current"`
}

func NewSnapshotManager(rustfs RustFSClient, tenantCode, workspaceCode string) *SnapshotManager {
    return &SnapshotManager{
        rustfs:        rustfs,
        bucket:        "workspaces",
        tenantCode:    tenantCode,
        workspaceCode: workspaceCode,
        maxSnapshots:  10,
        maxAge:        30 * 24 * time.Hour,
    }
}

func (s *SnapshotManager) prefix() string {
    return fmt.Sprintf("%s/%s", s.tenantCode, s.workspaceCode)
}
```

### 创建快照

```go
func (s *SnapshotManager) CreateSnapshot(ctx context.Context, workspaceDir, message string) (*SnapshotMeta, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    // 1. 打包 workspace
    tarData, checksum, fileCount, err := s.tarWorkspace(workspaceDir)
    if err != nil {
        return nil, fmt.Errorf("tar workspace: %w", err)
    }

    // 2. 生成快照 ID
    snapshotID := checksum[:8]

    // 3. 检查是否已存在（内容相同则跳过）
    if s.snapshotExists(ctx, snapshotID) {
        return s.getMetaByID(ctx, snapshotID), nil
    }

    // 4. 上传到 RustFS
    objectKey := fmt.Sprintf("%s/snap_%s.tar.gz", s.prefix(), snapshotID)
    if err := s.rustfs.PutObject(ctx, s.bucket, objectKey, tarData); err != nil {
        return nil, fmt.Errorf("upload snapshot: %w", err)
    }

    // 5. 更新元数据
    meta := &SnapshotMeta{
        ID:        snapshotID,
        Timestamp: time.Now(),
        Message:   message,
        FileCount: fileCount,
        Size:      int64(len(tarData)),
    }

    if err := s.appendMetadata(ctx, meta); err != nil {
        return nil, fmt.Errorf("update metadata: %w", err)
    }

    // 6. 异步清理旧快照
    go s.cleanupOldSnapshots(context.Background())

    return meta, nil
}
```

### 恢复快照

```go
func (s *SnapshotManager) RestoreSnapshot(ctx context.Context, snapshotID, workspaceDir string) error {
    // 1. 从 RustFS 下载快照
    objectKey := fmt.Sprintf("%s/snap_%s.tar.gz", s.prefix(), snapshotID)
    tarData, err := s.rustfs.GetObject(ctx, s.bucket, objectKey)
    if err != nil {
        return fmt.Errorf("download snapshot %s: %w", snapshotID, err)
    }

    // 2. 清空目标目录（保留 skills/）
    if err := s.clearWorkspace(workspaceDir); err != nil {
        return fmt.Errorf("clear workspace: %w", err)
    }

    // 3. 解压快照
    if err := s.untarWorkspace(tarData, workspaceDir); err != nil {
        return fmt.Errorf("untar workspace: %w", err)
    }

    // 4. 更新 current 指针
    if err := s.setCurrent(ctx, snapshotID); err != nil {
        return fmt.Errorf("update current: %w", err)
    }

    return nil
}
```

### 列出快照

```go
func (s *SnapshotManager) ListSnapshots(ctx context.Context) (*SnapshotList, error) {
    metaKey := fmt.Sprintf("%s/snapshots.json", s.prefix())
    data, err := s.rustfs.GetObject(ctx, s.bucket, metaKey)
    if err != nil {
        return &SnapshotList{}, nil // 首次，返回空列表
    }

    var list SnapshotList
    if err := json.Unmarshal(data, &list); err != nil {
        return nil, fmt.Errorf("parse metadata: %w", err)
    }

    return &list, nil
}
```

### 打包与解压

```go
func (s *SnapshotManager) tarWorkspace(dir string) ([]byte, string, int, error) {
    var buf bytes.Buffer
    gw := gzip.NewWriter(&buf)
    tw := tar.NewWriter(gw)

    hash := sha256.New()
    fileCount := 0

    err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }

        // 跳过 skills 目录（平台下发，不纳入版本）
        relPath, _ := filepath.Rel(dir, path)
        if strings.HasPrefix(relPath, "skills") {
            return nil
        }

        if info.IsDir() {
            return nil
        }

        fileCount++

        header, err := tar.FileInfoHeader(info, "")
        if err != nil {
            return err
        }
        header.Name = relPath

        if err := tw.WriteHeader(header); err != nil {
            return err
        }

        f, err := os.Open(path)
        if err != nil {
            return err
        }
        defer f.Close()

        writer := io.MultiWriter(tw, hash)
        if _, err := io.Copy(writer, f); err != nil {
            return err
        }

        return nil
    })

    tw.Close()
    gw.Close()

    if err != nil {
        return nil, "", 0, err
    }

    checksum := hex.EncodeToString(hash.Sum(nil))
    return buf.Bytes(), checksum, fileCount, nil
}

func (s *SnapshotManager) untarWorkspace(data []byte, dest string) error {
    gr, err := gzip.NewReader(bytes.NewReader(data))
    if err != nil {
        return err
    }
    defer gr.Close()

    tr := tar.NewReader(gr)

    for {
        header, err := tr.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }

        target := filepath.Join(dest, header.Name)

        if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
            return err
        }

        f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
        if err != nil {
            return err
        }

        if _, err := io.Copy(f, tr); err != nil {
            f.Close()
            return err
        }
        f.Close()
    }

    return nil
}
```

### 清理旧快照

```go
func (s *SnapshotManager) cleanupOldSnapshots(ctx context.Context) {
    list, err := s.ListSnapshots(ctx)
    if err != nil {
        return
    }

    // 1. 按数量限制清理
    if len(list.Snapshots) > s.maxSnapshots {
        toDelete := list.Snapshots[:len(list.Snapshots)-s.maxSnapshots]
        for _, snap := range toDelete {
            s.deleteSnapshot(ctx, snap.ID)
        }
        list.Snapshots = list.Snapshots[len(list.Snapshots)-s.maxSnapshots:]
    }

    // 2. 按时间限制清理（保留当前版本）
    var validSnapshots []SnapshotMeta
    for _, snap := range list.Snapshots {
        if snap.ID == list.Current {
            validSnapshots = append(validSnapshots, snap)
            continue
        }
        if time.Since(snap.Timestamp) < s.maxAge {
            validSnapshots = append(validSnapshots, snap)
        } else {
            s.deleteSnapshot(ctx, snap.ID)
        }
    }
    list.Snapshots = validSnapshots

    // 3. 保存更新后的元数据
    s.saveMetadata(ctx, list)
}

func (s *SnapshotManager) deleteSnapshot(ctx context.Context, snapshotID string) {
    objectKey := fmt.Sprintf("%s/snap_%s.tar.gz", s.prefix(), snapshotID)
    s.rustfs.DeleteObject(ctx, s.bucket, objectKey)
}
```

### 辅助方法

```go
func (s *SnapshotManager) snapshotExists(ctx context.Context, snapshotID string) bool {
    objectKey := fmt.Sprintf("%s/snap_%s.tar.gz", s.prefix(), snapshotID)
    _, err := s.rustfs.StatObject(ctx, s.bucket, objectKey)
    return err == nil
}

func (s *SnapshotManager) appendMetadata(ctx context.Context, meta *SnapshotMeta) error {
    list, _ := s.ListSnapshots(ctx)
    list.Snapshots = append(list.Snapshots, *meta)
    list.Current = meta.ID
    return s.saveMetadata(ctx, list)
}

func (s *SnapshotManager) saveMetadata(ctx context.Context, list *SnapshotList) error {
    data, err := json.MarshalIndent(list, "", "  ")
    if err != nil {
        return err
    }
    metaKey := fmt.Sprintf("%s/snapshots.json", s.prefix())
    return s.rustfs.PutObject(ctx, s.bucket, metaKey, data)
}

func (s *SnapshotManager) setCurrent(ctx context.Context, snapshotID string) error {
    list, err := s.ListSnapshots(ctx)
    if err != nil {
        return err
    }
    list.Current = snapshotID
    return s.saveMetadata(ctx, list)
}

func (s *SnapshotManager) clearWorkspace(workspaceDir string) error {
    entries, err := os.ReadDir(workspaceDir)
    if err != nil {
        return err
    }

    for _, entry := range entries {
        if entry.Name() == "skills" {
            continue
        }
        path := filepath.Join(workspaceDir, entry.Name())
        os.RemoveAll(path)
    }

    return nil
}
```

## API 接口

```go
// 列出快照
func SnapshotList(ctx context.Context, req *SnapshotListReq) (*SnapshotListResp, error) {
    uc := ctxs.GetUserCtx(ctx)
    if !isPlatformAdmin(uc) && uc.TenantCode != req.TenantCode {
        return nil, errors.PermissionDenied
    }

    mgr := NewSnapshotManager(svcCtx.RustFS, req.TenantCode, req.WorkspaceCode)
    list, err := mgr.ListSnapshots(ctx)
    if err != nil {
        return nil, err
    }

    return &SnapshotListResp{
        Snapshots: list.Snapshots,
        Current:   list.Current,
    }, nil
}

// 回滚快照
func SnapshotRollback(ctx context.Context, req *SnapshotRollbackReq) (*SnapshotRollbackResp, error) {
    uc := ctxs.GetUserCtx(ctx)
    if !isPlatformAdmin(uc) && uc.TenantCode != req.TenantCode {
        return nil, errors.PermissionDenied
    }

    workspaceDir := fmt.Sprintf("/data/workspaces/%s/%s", req.TenantCode, req.WorkspaceCode)
    mgr := NewSnapshotManager(svcCtx.RustFS, req.TenantCode, req.WorkspaceCode)

    if err := mgr.RestoreSnapshot(ctx, req.SnapshotID, workspaceDir); err != nil {
        return nil, err
    }

    return &SnapshotRollbackResp{Success: true}, nil
}
```

## 性能指标

| 指标 | 典型值 |
|------|--------|
| 单个 Workspace 快照存储 | ~130KB（10 个快照） |
| 创建快照耗时 | < 100ms |
| 列出快照耗时 | < 50ms |
| 恢复快照耗时 | < 200ms |
| 最大快照数 | 10（可配置） |
| 快照保留时间 | 30 天（可配置） |

## 业务案例

### 案例 1：大鹏告警（定时触发型）

**场景**：根据大鹏的状态调整开窗频率及是否告警。

**Workspace 文件**：

```
workspace/
├── agent.md          # "设备监控助手职责说明"
├── learned.md        # "上次阈值 80% 效果最好"
├── code/
│   └── main.py      # 阈值计算、频率决策逻辑
└── skills/
    ├── get-device-status/
    ├── get-weather/
    ├── control-device/
    └── send-alert/
```

**自学习场景**：

```
执行发现：高温天气下阈值 80% 过于保守
     ↓
建议：根据天气预报动态调整阈值
     ↓
更新 code/main.py：
  - 新增 get_weather() 调用
  - 新增动态阈值计算：threshold = base * weather_factor
     ↓
更新 learned.md：
  - 记录："高温天气（>35℃）阈值应降至 70%"
     ↓
创建快照："优化高温天气阈值逻辑"
```

### 案例 2：文档处理（用户触发型）

**场景**：AI 辅助填写 OCR 识别后的数据到 Excel。

**用户反馈优化流程**：

```
用户发现识别不对，更新后再次调用
     ↓
判断是否可以优化
  → 对比用户修正 vs 原始结果
  → 分析：是规则缺失？模型偏差？
     ↓
可以优化 → 更新 code 和 learned.md
  → 更新 code/main.py：新增纠错规则
  → 更新 learned.md：记录 OCR 纠错规则
  → 创建快照："新增 OCR 纠错规则"
     ↓
下次处理自动应用新规则
```

### 案例 3：审核（自动/手动触发型）

**场景**：AI 辅助审核 Excel 数据。

**审核员调整后自学习流程**：

```
审核员在 AI 审核基础上做调整
     ↓
调整完提交后触发自学习
     ↓
根据审核结果更新 code 和 learned.md
  → 分析：审核员改了什么？为什么改？
  → 更新 code/main.py：调整阈值参数、新增边界条件
  → 更新 learned.md：记录审核规则变更
  → 创建快照："审核规则优化"
     ↓
下次审核自动应用新规则
```

### 案例 4：关联文档更新（用户触发型）

**场景**：用户修改报告，AI 协助更新关联的多个文档。

**迭代过程示意**：

```
第 1 轮：
  用户：项目周期 30天→45天
  AI：更新了周报、进度表、风险报告
  用户：进度表的计算公式没更新

第 2 轮：
  AI：修正进度表计算公式
  用户：确认无误

完成：
  更新 code 和 agent.md
  创建快照："优化关联文档更新逻辑"
```

### 案例对比总结

| 案例 | 触发方式 | 入参 | 主要 Skills | Code 作用 | 自学习点 |
|------|---------|------|------------|----------|---------|
| 大鹏告警 | 定时任务 | 无 | 设备状态、天气、告警、设备控制 | 阈值计算、频率决策 | 动态阈值优化 |
| 文档处理 | 用户触发 | OCR结果、Excel | 文件下载、知识库检索 | 数据填写、验证 | OCR纠错规则 |
| 审核 | 用户/自动 | Excel、项目信息 | 文件下载、知识库检索 | 测试验算 | 审核规则优化 |
| 关联文档更新 | 用户触发 | 更新内容、基础文档 | 知识库检索、文件下载 | 文档生成、一致性检查 | 更新模板优化 |

## 扩展设计

### 增量快照（未来优化）

```
snap_001.tar.gz        # 全量快照
snap_002.diff.tar.gz   # 增量（只含变更）
snap_003.diff.tar.gz   # 增量
```

### 快照标签

```json
{
  "id": "e5f6g7h8",
  "tags": ["stable", "production"],
  "message": "稳定版本"
}
```

## 文件清单

| 文件 | 说明 |
|------|------|
| `pkg/workspace/snapshot.go` | 快照管理器核心实现 |
| `internal/logic/workspace/snapshot*.go` | API 接口实现 |
