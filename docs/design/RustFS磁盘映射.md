# RustFS 磁盘映射方案

## 方案概述

RustFS 是一个高性能、S3 兼容的对象存储服务（MinIO 的 Apache 2.0 替代品，用 Rust 实现），用于为 sandbox 提供持久化存储后端。

**定位**：
- RustFS 作为每个租户的 workspace 存储后端
- **租户隔离粒度：共享 bucket + tenantCode prefix**
- 容器按需启动时，从对应租户 bucket 下载 workspace；退出时上传变更回 bucket

## 核心特性

- **用户态实现**：基于 FUSE（Filesystem in Userspace），无需内核模块
- **高性能**：Rust 零成本抽象，内存安全保证
- **透明挂载**：应用程序无感知，直接使用标准文件 I/O
- **多后端支持**：本地磁盘、对象存储（S3/OSS）、分布式存储

## 技术架构

### 架构分层

```
┌─────────────────────────────────────┐
│   应用层（用户应用）                 │
├─────────────────────────────────────┤
│   VFS 层（标准 POSIX 文件接口）     │
├─────────────────────────────────────┤
│   FUSE 层（内核 FUSE 驱动）         │
├─────────────────────────────────────┤
│   RustFS 用户态守护进程              │
│   ├─ 文件系统逻辑                   │
│   ├─ 缓存管理                       │
│   └─ 后端适配器                     │
├─────────────────────────────────────┤
│   存储后端（本地/对象存储/分布式）   │
└─────────────────────────────────────┘
```

### 核心组件

#### FUSE 接口层
- 实现 FUSE 协议的文件系统操作：`lookup`、`read`、`write`、`mkdir`、`unlink` 等
- 使用 `fuser` crate（Rust FUSE 库）

#### 缓存管理
- **元数据缓存**：inode、目录结构、文件属性
- **数据缓存**：热点文件内容缓存（LRU 策略）
- **写缓冲**：异步写入，批量刷盘

#### 后端适配器
- **本地磁盘**：直接映射到宿主机目录
- **对象存储**：S3/OSS API 适配
- **分布式存储**：Ceph/GlusterFS 客户端

## 实现方案

### 方案一：纯本地磁盘映射（MVP）

**适用场景**：单机部署、开发测试环境

```rust
struct RustFS {
    backend_path: PathBuf,      // 宿主机实际存储路径
    mount_point: PathBuf,       // 容器内挂载点
    cache: Arc<RwLock<Cache>>,  // 元数据缓存
}

// 挂载示例
rustfs mount \
  --backend /data/storage \
  --mount /mnt/workspace \
  --cache-size 1GB
```

**优势**：实现简单，性能最优，无网络开销，适合快速验证。

**劣势**：无法跨节点共享，依赖宿主机磁盘容量。

### 方案二：对象存储映射（生产推荐）

**适用场景**：多节点集群、云原生部署

```rust
struct S3Backend {
    client: S3Client,
    bucket: String,
    prefix: String,
    cache: LocalCache,  // 本地缓存层
}

// 挂载示例
rustfs mount \
  --backend s3://my-bucket/workspace-data \
  --mount /mnt/workspace \
  --cache-dir /tmp/rustfs-cache \
  --cache-size 10GB \
  --access-key $AWS_ACCESS_KEY \
  --secret-key $AWS_SECRET_KEY
```

**优势**：容量无限扩展，多节点共享数据，数据持久化保证。

**劣势**：网络延迟（通过缓存优化），对象存储费用。

### 方案三：混合存储（高级）

**适用场景**：大规模生产环境

- **热数据**：本地 SSD 缓存
- **温数据**：对象存储
- **冷数据**：归档存储（Glacier）

```rust
struct TieredBackend {
    hot: LocalDisk,
    warm: S3Backend,
    cold: GlacierBackend,
    policy: TieringPolicy,  // 自动分层策略
}
```

## 与 Sandbox Workspace 集成

RustFS 作为对象存储后端（S3 兼容），**不使用 FUSE 挂载**。启动容器前，通过 S3 SDK 将租户 bucket 内容同步到本地磁盘：

```go
func syncFromS3(ctx context.Context, tenantCode, localDir string) error {
    bucket := "workspace-" + tenantCode
    return ossClient.Bucket(bucket).SyncToLocal(ctx, localDir)
}
```

### 生命周期管理

- **启动前**：从 RustFS bucket 增量同步 workspace 到本地磁盘
- **运行中**：直接读写本地磁盘，fsnotify 异步上传变更到 RustFS
- **退出后**：diff 补漏，将未捕获的变更文件上传回 RustFS

## 性能优化

### 缓存策略

```rust
struct CacheConfig {
    metadata_ttl: Duration,      // 元数据缓存时间：5s
    data_cache_size: usize,      // 数据缓存大小：1GB
    prefetch_enabled: bool,      // 预读取：true
    write_buffer_size: usize,    // 写缓冲：64MB
}
```

### 并发控制

- 使用 `tokio` 异步运行时
- 读操作并发无锁
- 写操作细粒度锁（文件级）

### 网络优化（对象存储）

- **分片上传**：大文件分片并行上传
- **断点续传**：网络中断自动重试
- **连接池**：复用 HTTP 连接

## 安全性

### 权限控制

```rust
struct PermissionConfig {
    uid: u32,           // 文件所有者 UID
    gid: u32,           // 文件所属组 GID
    file_mode: u16,     // 文件权限：0644
    dir_mode: u16,      // 目录权限：0755
}
```

### 数据加密

- **传输加密**：HTTPS/TLS
- **存储加密**：AES-256（可选）
- **密钥管理**：环境变量或密钥管理服务

## 监控与运维

### 指标暴露

```rust
struct Metrics {
    read_ops: Counter,          // 读操作次数
    write_ops: Counter,         // 写操作次数
    cache_hit_rate: Gauge,      // 缓存命中率
    latency_p99: Histogram,     // P99 延迟
}
```

### 日志记录

- **结构化日志**：JSON 格式
- **日志级别**：ERROR/WARN/INFO/DEBUG
- **审计日志**：文件访问记录

## 实施路线图

### Phase 1：MVP（2 周）
- [ ] 实现本地磁盘映射
- [ ] 基础 FUSE 操作（read/write/mkdir）
- [ ] 简单元数据缓存
- [ ] S3 SDK 增量同步验证

### Phase 2：生产化（4 周）
- [ ] S3 后端适配器
- [ ] 完整缓存策略
- [ ] 错误处理与重试
- [ ] 监控指标集成

### Phase 3：优化（4 周）
- [ ] 性能调优
- [ ] 混合存储支持
- [ ] 高可用设计
- [ ] 压力测试

## 技术栈

- **语言**：Rust 1.75+
- **核心库**：
  - `fuser`：FUSE 绑定
  - `tokio`：异步运行时
  - `aws-sdk-s3`：S3 客户端
  - `serde`：序列化
  - `tracing`：日志追踪

## 版本管理注意事项

### 版本控制粒度限制

- 版本控制是 **Bucket 级别**的，S3 规范不支持 Prefix 级别的版本开关
- 开启后对 bucket 内**所有对象**生效，无法只对特定目录开启
- 如需不同目录独立控制，唯一方案是**拆分 Bucket**

### 磁盘消耗警告

> **版本控制会成倍增加磁盘消耗，必须配合生命周期规则使用，否则磁盘将无上限增长。**

每个版本存储完整文件，不是增量 diff：

```
# 示例：1GB 的模型文件每天覆写一次
无生命周期规则：365 天 = 365GB
设置保留 3 个版本：始终只有 3GB
```

### 必须同步配置生命周期规则

开启版本控制时，**必须**同时配置以下生命周期规则：

```bash
aws s3api put-bucket-lifecycle-configuration \
  --endpoint-url http://rustfs:9000 \
  --bucket my-bucket \
  --lifecycle-configuration '{
    "Rules": [
      {
        "ID": "limit-versions",
        "Filter": {"Prefix": ""},
        "Status": "Enabled",
        "NoncurrentVersionExpiration": {
          "NoncurrentDays": 30,
          "NewerNoncurrentVersions": 3
        },
        "ExpiredObjectDeleteMarker": true
      }
    ]
  }'
```

关键参数说明：
- `NoncurrentDays: 30`：旧版本 30 天后自动删除
- `NewerNoncurrentVersions: 3`：最多保留 3 个历史版本
- `ExpiredObjectDeleteMarker: true`：清理孤立的删除标记，避免额外占用

### 按前缀差异化保留策略

如果不同目录需要不同的版本保留时长，通过多条规则实现：

```bash
# 模型文件：保留 3 个版本，90 天过期
{"Filter": {"Prefix": "models/"}, "NoncurrentVersionExpiration": {"NoncurrentDays": 90, "NewerNoncurrentVersions": 3}}

# 日志文件：保留 1 天
{"Filter": {"Prefix": "logs/"}, "NoncurrentVersionExpiration": {"NoncurrentDays": 1}}

# 缓存文件：立即过期
{"Filter": {"Prefix": "cache/"}, "NoncurrentVersionExpiration": {"NoncurrentDays": 0}}
```

## 风险与挑战

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| FUSE 性能开销 | 用户态文件系统比内核 FS 慢 10-30% | 激进缓存策略、异步 I/O |
| 对象存储延迟 | 网络 RTT 影响小文件性能 | 本地缓存、批量操作 |
| 一致性问题 | 多节点并发写入冲突 | 文件锁机制、最终一致性模型 |
| 运维复杂度 | 需管理本地磁盘缓存目录 | 统一管理 workspace 目录生命周期、定期清理过期租户数据 |

## 参考资源

- [FUSE 协议文档](https://www.kernel.org/doc/html/latest/filesystems/fuse.html)
- [fuser crate](https://github.com/cberner/fuser)
- [S3FS 实现参考](https://github.com/s3fs-fuse/s3fs-fuse)
- [JuiceFS 架构](https://juicefs.com/docs/community/architecture)
