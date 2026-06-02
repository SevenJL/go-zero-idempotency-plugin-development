# go-zero 分布式通用幂等性插件

基于 DDD 领域驱动设计的分布式通用幂等性插件，以 go-zero 为主接入框架，同时支持 Gin、标准 net/http、原生 gRPC。核心引擎与框架无关，适配层尽量薄。

## 代码进度

- ✅ M1 领域层：聚合根、值对象、领域策略、仓储端口
- ✅ M2 应用层：`IdempotencyService`、command、DTO、默认端口实现、Memory 仓储
- ✅ M3 Redis 仓储：Lua 原子脚本（Begin/Commit/Abort/Renew）、JSON record mapper
- ✅ M4 HTTP 适配器：net/http、go-zero、Gin 中间件，响应捕获与 replay
- ✅ M5 gRPC 适配器：UnaryServerInterceptor、RPCCodec 端口与注册表
- ✅ M6 可观测性：Logger/Metrics/Tracer 端口 + no-op 默认实现
- ✅ 响应缓存策略：CaptureRules 领域服务（状态码/Content-Type/body size 规则）
- ✅ ProcessingTTL 自动续期：Heartbeat 组件，防止长耗时接口死锁

## 目录结构

```
├── domain/                          # 领域层（不依赖任何框架）
│   ├── model/                       # IdempotencyRecord 聚合根、状态机、决策
│   ├── valueobject/                 # IdempotencyKey、Fingerprint、Owner 等值对象
│   ├── service/                     # IdempotencyPolicy、CaptureRules 领域服务
│   └── repository/                  # IdempotencyRecordRepository 仓储端口
├── application/                     # 应用层（编排用例，不依赖基础设施）
│   ├── command/                     # Begin/Complete/Abort/Replay 命令
│   ├── dto/                         # RequestContext、CapturedResponse、BeginResult
│   ├── port/                        # KeyResolver、Fingerprinter、Clock、Logger 等端口
│   └── service/                     # IdempotencyService、Heartbeat、Config、默认端口实现
├── infrastructure/                  # 基础设施层（实现领域和应用端口）
│   ├── persistence/
│   │   ├── memory/                  # 内存仓储（单测与本地调试）
│   │   └── redis/                   # Redis 仓储（Lua 原子脚本，支持分布式部署）
│   └── codec/                       # JSON codec + RPCCodecRegistry
├── interfaces/                      # 接口层（框架适配器）
│   └── middleware/
│       ├── httpx/                   # net/http 标准中间件 + 响应捕获
│       ├── gozero/                  # go-zero rest.Middleware
│       ├── gin/                     # Gin middleware
│       └── grpc/                    # gRPC UnaryServerInterceptor
├── tests/                           # 集成测试（25 个场景，全部通过）
└── docs/                            # DDD 设计和开发文档
```

## 快速开始

### 安装

```bash
go get github.com/sevenjl/go-zero-idempotency-plugin-development
```

### go-zero HTTP

```go
import (
    "github.com/zeromicro/go-zero/core/stores/redis"
    "github.com/zeromicro/go-zero/rest"

    appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
    "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/redis"
    gozerohttp "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/gozero"
)

// Composition root
rds := redis.MustNewRedis(redis.RedisConf{...})
repo := redisrepo.NewIdempotencyRecordRepository(rds, redisrepo.WithKeyPrefix("idem"))
idemSvc, _ := appservice.NewIdempotencyService(appservice.Config{
    Repository: repo,
    Scope:      "order-api",
})

server := rest.MustNewServer(rest.RestConf{...})
server.Use(gozerohttp.Middleware(idemSvc))
```

### Gin

```go
import (
    "github.com/gin-gonic/gin"
    ginidem "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/gin"
)

r := gin.New()
r.Use(ginidem.Middleware(idemSvc))
r.POST("/api/orders", createOrder)
```

### net/http

```go
import (
    "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/httpx"
)

mux := http.NewServeMux()
mux.Handle("/api/orders", httpx.Middleware(idemSvc)(http.HandlerFunc(createOrder)))
```

### gRPC / go-zero zrpc

```go
import (
    "google.golang.org/grpc"
    grpcidem "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/grpc"
    "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/codec"
)

registry := codec.NewCodecRegistry(nil)
registry.Register("/order.OrderService/Create", codec.JSONCodec{}, func() any {
    return &orderpb.CreateOrderResp{}
})

s := grpc.NewServer(
    grpc.UnaryInterceptor(grpcidem.UnaryServerInterceptor(idemSvc, registry)),
)
```

## 核心架构

```
client → interfaces (HTTP/RPC 适配器)
              ↓
         application (IdempotencyService 编排)
              ↓
         domain (聚合根、值对象、领域策略)
              ↓
         infrastructure (Redis/Memory 仓储)
```

依赖方向：`interfaces → application → domain`，`infrastructure → domain/application ports`

## 运行测试

```bash
go test ./... -count=1
```

## 性能报告

测试环境：MacBook Air M3 / Go 1.25.1 / Gin release mode / Memory 仓储  
测试工具：[go-wrk](https://github.com/tsliwowicz/go-wrk) — 50 并发连接, 10 秒预热, 10 秒压测  
被测端点：`POST /api/orders`（Gin + idempotency middleware）

### 吞吐量与延迟

| 场景 | 请求/秒 | 平均延迟 | 最慢请求 | 说明 |
|---|---|---|---|---|
| **Baseline** (无 Key，放行) | 54,907 | 910µs | 9.6ms | 裸 Gin handler，0 额外开销 |
| **Acquire** (新 Key，首次获取) | 53,841 | 928µs | 9.3ms | 创建幂等记录 + 执行 handler + Complete |
| **Replay** (同 Key，缓存命中) | 53,310 | 937µs | 15.9ms | 返回缓存的 201 响应，不执行 handler |
| **Conflict** (同 Key，指纹冲突) | 53,030\* | — | — | 返回 409，不执行 handler |

> \* 估算值：530,295 errors / 10s。go-wrk 将非 2xx 计为 error 不计入统计。

### 开销分析

```
Baseline → Acquire:   +18µs (+2.0%)    创建记录 + SHA-256 指纹 + handler
Acquire  → Replay :    +9µs (+1.0%)    从内存读取 + 深拷贝 + 写回响应
Acquire  → Conflict:    ~0µs            同路径（仅比较指纹）
```

- **p50 延迟 ~130µs**，p99（快路径）~150µs，不含 Tail Latency
- 峰值 QPS 约 **5.5 万/秒**（单机 MacBook Air）
- Memory 仓储 mutex 未成为瓶颈（50 并发下无争用）
- 最大开销来自 SHA-256 指纹计算 + JSON canonicalization

### Memory 仓储 vs Redis 仓储预估

| 指标 | Memory 仓储 | Redis 仓储（预估） |
|---|---|---|
| 延迟 | +18µs | +0.5–2ms（网络 RTT） |
| QPS | 5.5 万 | 取决于 Redis 集群规格 |
| 一致性 | mutex（单进程） | Lua 原子脚本（分布式） |
| 适用场景 | 本地调试、单测 | 生产多副本部署 |

> Redis 仓储的实际性能取决于网络延迟和 Redis 实例规格。建议在生产环境使用 `go-wrk` 复测。

## 配置说明

### IdempotencyService Config

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `Disabled` | `bool` | `false` | 显式禁用插件 |
| `Scope` | `string` | `""` | 服务标识，参与指纹计算 |
| `Repository` | `IdempotencyRecordRepository` | **必填** | 仓储实现 |
| `Policy.DuplicatePolicy` | `string` | `"reject"` | 重复请求策略：`reject` / `wait` / `pass_through` |
| `Policy.TTL.ProcessingTTL` | `duration` | `30s` | 处理中记录 TTL |
| `Policy.TTL.CompletedTTL` | `duration` | `24h` | 完成记录缓存 TTL |
| `Policy.TTL.FailedTTL` | `duration` | `5m` | 失败记录缓存 TTL |
| `WaitTimeout` | `duration` | `5s` | Wait 策略最大等待时间 |
| `WaitInterval` | `duration` | `50ms` | Wait 策略轮询间隔 |
| `CaptureRules.CacheStatus2xx` | `bool` | `true` | 缓存成功响应 |
| `CaptureRules.CacheStatus5xx` | `bool` | `false` | 缓存服务端错误响应 |
| `CaptureRules.MaxBodyBytes` | `int64` | `1MB` | 最大缓存 body 大小 |
| `CaptureRules.ContentTypes` | `[]string` | `["application/json"]` | 允许缓存的 Content-Type |
| `CaptureRules.ExcludedHeaders` | `[]string` | `Set-Cookie,Authorization,Cookie` | 不缓存的响应头 |
| `Logger` | `port.Logger` | no-op | 结构化日志 |
| `Metrics` | `port.Metrics` | no-op | 指标上报 |
| `Tracer` | `port.Tracer` | no-op | 分布式追踪 |
| `KeyResolver` | `port.KeyResolver` | `HeaderKeyResolver` | 幂等键提取 |
| `Fingerprinter` | `port.Fingerprinter` | `SHA256Fingerprinter` | 请求指纹计算 |

### Redis 仓储选项

```go
repo := redisrepo.NewIdempotencyRecordRepository(
    rds,
    redisrepo.WithKeyPrefix("idem"),
)
```

## 可观测性指标

插件上报以下 Prometheus 指标：

| 指标名 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `idempotency_commit_total` | Counter | `result` (success/error) | 提交计数 |

日志字段（结构化）：

| 字段 | 说明 |
|---|---|
| `key_hash` | 幂等键 SHA-256 前 12 位 hex（不记录原始 key） |
| `error` | 错误信息 |

## 文档入口

- [基于 go-zero 的分布式通用幂等性插件开发文档](docs/idempotency-plugin-development.md)

## 里程碑进度

| 里程碑 | 内容 | 状态 |
|---|---|---|
| M1 | 领域层：聚合根、值对象、领域服务、仓储端口 | ✅ 完成 |
| M2 | 应用层与 Memory 仓储：IdempotencyService、command、DTO | ✅ 完成 |
| M3 | Redis 仓储：Lua 原子脚本、JSON record mapper | ✅ 完成 |
| M4 | HTTP 适配器：net/http、go-zero、Gin 中间件 | ✅ 完成 |
| M5 | gRPC 适配器：UnaryInterceptor、RPCCodec 端口与注册表 | ✅ 完成 |
| M6 | 可观测性：Logger/Metrics/Tracer 端口 | ✅ 完成 |
| + | 响应缓存策略：CaptureRules 领域服务 | ✅ 完成 |
| + | TTL 自动续期：Heartbeat 组件 | ✅ 完成 |
