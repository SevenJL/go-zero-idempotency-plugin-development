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
│   ├── port/                        # KeyResolver、Fingerprinter、Clock、Logger、Notifier 等端口
│   └── service/                     # IdempotencyService、Heartbeat、Config、ConfigWatcher、默认端口实现
├── infrastructure/                  # 基础设施层（实现领域和应用端口）
│   ├── persistence/
│   │   ├── memory/                  # 内存仓储（单测与本地调试）
│   │   ├── redis/                   # Redis 仓储（Lua 脚本、AES 加密、Pub/Sub、Sentinel、熔断器）
│   │   └── sql/                     # SQL 仓储（MySQL/PostgreSQL，原子 INSERT ON DUPLICATE KEY）
│   ├── observability/               # OTel Logger/Metrics/Tracer 适配器 + go-zero 实现
│   └── codec/                       # JSON codec + RPCCodecRegistry
├── interfaces/                      # 接口层（框架适配器）
│   └── middleware/
│       ├── httpx/                   # net/http 标准中间件 + 响应捕获
│       ├── gozero/                  # go-zero rest.Middleware
│       ├── gin/                     # Gin middleware
│       └── grpc/                    # gRPC UnaryServerInterceptor
├── tests/                           # 单元 + 集成测试（25 场景） + Redis 集成测试（10 场景，build tag）
├── examples/
│   ├── gin/                         # Gin 示例（Web UI、健康检查、pprof、优雅关闭）
│   ├── gozero-http/                 # go-zero HTTP 示例
│   └── grpc/                        # gRPC 原生示例
├── docs/                            # DDD 设计文档
├── deploy/                          # DevOps 资产
│   ├── helm/idempotency-example/    # Kubernetes Helm Chart
│   ├── grafana-dashboard.json       # Grafana 监控面板
│   └── swagger.json                 # OpenAPI 3.0 接口规范
├── scripts/                         # 自动化脚本
│   ├── benchmark.sh                 # go-wrk 压测套件
│   └── run-tests.sh                 # 测试运行器
└── .github/workflows/               # CI/CD 流水线
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
    redisrepo "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/redis"
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

### YAML 配置方式

```go
// 在 go-zero 的 service context 中嵌入 ConfigFile
type Config struct {
    rest.RestConf
    Idempotency appservice.ConfigFile `json:",optional" yaml:",optional"`
}

// 在 main 中加载
var c Config
conf.MustLoad("etc/config.yaml", &c)
idemCfg, _ := c.Idempotency.ToServiceConfig(repo, logger, metrics, tracer)
idemSvc, _ := appservice.NewIdempotencyService(idemCfg)

// 配置热加载
watcher, _ := appservice.WatchConfig("etc/config.yaml", func(cfg appservice.ConfigFile) {
    newCfg, _ := cfg.ToServiceConfig(repo, logger, metrics, tracer)
    svc.Reconfigure(newCfg)
})
defer watcher.Close()
```

### Gin

```go
r := gin.New()
r.Use(ginidem.Middleware(idemSvc))
r.POST("/api/orders", createOrder)
```

### net/http

```go
mux := http.NewServeMux()
mux.Handle("/api/orders", httpx.Middleware(idemSvc)(http.HandlerFunc(createOrder)))
```

### gRPC / go-zero zrpc

```go
registry := codec.NewCodecRegistry(nil)
registry.Register("/order.OrderService/Create", codec.JSONCodec{}, func() any {
    return &orderpb.CreateOrderResp{}
})

s := grpc.NewServer(
    grpc.UnaryInterceptor(grpcidem.UnaryServerInterceptor(idemSvc, registry)),
)
```

## 存储后端

| 后端 | 适用场景 | 原子性 |
| --- | --- | --- |
| **Memory** | 本地调试、单元测试 | sync.Mutex |
| **Redis** | 分布式生产部署 | Lua 脚本 |
| **Redis Sentinel** | 高可用生产部署 | Lua 脚本 + 自动故障转移 |
| **MySQL** | 无需 Redis 的分布式部署 | INSERT ON DUPLICATE KEY |
| **PostgreSQL** | 无需 Redis 的分布式部署 | INSERT ON CONFLICT DO NOTHING |

```go
// Redis Sentinel
client, _ := redisrepo.NewSentinelClient(redisrepo.SentinelConfig{
    MasterName:    "mymaster",
    SentinelAddrs: []string{"sentinel-1:26379", "sentinel-2:26379"},
})
repo := redisrepo.NewIdempotencyRecordRepository(client, redisrepo.WithKeyPrefix("idem"))

// SQL (MySQL)
db, _ := sql.Open("mysql", dsn)
repo := sqlrepo.NewIdempotencyRecordRepository(db, sqlrepo.DriverMySQL)

// SQL (PostgreSQL)
db, _ := sql.Open("postgres", dsn)
repo := sqlrepo.NewIdempotencyRecordRepository(db, sqlrepo.DriverPostgres)
```

## 核心架构

```text
client → interfaces (HTTP/RPC 适配器)
              ↓
         application (IdempotencyService 编排)
              ↓
         domain (聚合根、值对象、领域策略)
              ↓
         infrastructure (Redis/SQL/Memory 仓储)
```

依赖方向：`interfaces → application → domain`，`infrastructure → domain/application ports`

## 运行测试

### 单元与集成测试

```bash
go test ./... -count=1

# 或使用脚本
./scripts/run-tests.sh
```

### Redis 集成测试

```bash
# 启动 Redis
docker compose up -d redis

# 运行 Redis 集成测试
REDIS_ADDR=localhost:6379 go test -tags=integration -count=1 -v ./tests/
```

### 性能基准测试

```bash
# 启动示例服务后运行
./scripts/benchmark.sh

# 快速模式（5s per scenario）
./scripts/benchmark.sh --quick
```

## 性能报告

测试环境：MacBook Air M4 / Go 1.25.1 / Gin release mode / Memory 仓储
测试工具：[go-wrk](https://github.com/tsliwowicz/go-wrk) — 50 并发连接

| 场景 | 请求/秒 | 平均延迟 | 说明 |
| --- | --- | --- | --- |---|
| **Baseline** (无 Key，放行) | 54,907 | 910µs | 裸 Gin handler，0 额外开销 |
| **Acquire** (新 Key，首次获取) | 53,841 | 928µs | 创建幂等记录 + 执行 handler + Complete |
| **Replay** (同 Key，缓存命中) | 53,310 | 937µs | 返回缓存响应，不执行 handler |
| **Conflict** (同 Key，指纹冲突) | 53,030\* | — | 返回 409 |

> 开销：Acquire +18µs (+2.0%)，Replay +9µs (+1.0%)

## 配置说明

### IdempotencyService Config

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |---|
| `Disabled` | `bool` | `false` | 显式禁用插件 |
| `Scope` | `string` | `""` | 服务标识，参与指纹计算 |
| `Repository` | `IdempotencyRecordRepository` | **必填** | 仓储实现 |
| `Policy.DuplicatePolicy` | `string` | `"reject"` | 重复请求策略：`reject` / `wait` / `pass_through` |
| `Policy.TTL.ProcessingTTL` | `duration` | `30s` | 处理中记录 TTL |
| `Policy.TTL.CompletedTTL` | `duration` | `24h` | 完成记录缓存 TTL |
| `Policy.TTL.FailedTTL` | `duration` | `5m` | 失败记录缓存 TTL |
| `WaitTimeout` | `duration` | `5s` | Wait 策略最大等待时间 |
| `WaitInterval` | `duration` | `50ms` | Wait 策略轮询间隔 |
| `CaptureRules.MaxBodyBytes` | `int64` | `1MB` | 最大缓存 body 大小 |
| `CaptureRules.ContentTypes` | `[]string` | `["application/json"]` | 允许缓存的 Content-Type |
| `CaptureRules.ExcludedHeaders` | `[]string` | `Set-Cookie,Authorization,Cookie` | 不缓存的响应头 |
| `Logger` | `port.Logger` | no-op | 结构化日志 |
| `Metrics` | `port.Metrics` | no-op | 指标上报 |
| `Tracer` | `port.Tracer` | no-op | 分布式追踪 |
| `Notifier` | `port.Notifier` | no-op | Pub/Sub 事件通知 |
| `KeyResolver` | `port.KeyResolver` | `HeaderKeyResolver` | 幂等键提取 |
| `Fingerprinter` | `port.Fingerprinter` | `SHA256Fingerprinter` | 请求指纹计算 |

### Redis 仓储选项

```go
repo := redisrepo.NewIdempotencyRecordRepository(
    rds,
    redisrepo.WithKeyPrefix("idem"),           // 键前缀
    redisrepo.WithHashTag("order-api"),         // Redis Cluster hash tag
    redisrepo.WithBodyEncryptor(encryptor),     // AES-GCM 响应体加密
    redisrepo.WithPubSubNotifier(notifier),     // Pub/Sub 事件通知
    redisrepo.WithBreakerMaxFailures(5),        // 熔断器阈值
    redisrepo.WithBreakerCooldown(30*time.Second), // 熔断冷却时间
    redisrepo.WithStorageFailureMode("fail_closed"), // fail_closed | fail_open
)
```

## 可观测性

### Prometheus 指标

| 指标名 | 类型 | 标签 | 说明 |
| --- | --- | --- | --- |---|
| `idempotency_begin_total` | Counter | `result_type` | Begin 调用计数 |
| `idempotency_commit_total` | Counter | `result` | Commit 成功/失败计数 |
| `idempotency_replay_total` | Counter | — | Replay 命中计数 |
| `idempotency_storage_errors_total` | Counter | — | 存储错误计数 |
| `idempotency_wait_seconds` | Histogram | `result_type` | Wait 等待耗时 |
| `idempotency_record_bytes` | Histogram | `result` | 记录体大小 |

### Grafana Dashboard

预构建面板见 `deploy/grafana-dashboard.json`，包含 10 个面板：Begin Rate、Commit Rate、Replay Rate、Storage Errors、Cache Hit Ratio、Throughput、Wait Latency、Record Size 等。

### pprof 性能分析

示例项目内置 pprof 端点：

```bash
# Goroutine dump
curl http://localhost:8080/debug/pprof/goroutine?debug=1

# 30s CPU profile
curl http://localhost:8080/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# Heap profile
go tool pprof http://localhost:8080/debug/pprof/heap
```

## 文档入口

- [基于 go-zero 的分布式通用幂等性插件开发文档](docs/idempotency-plugin-development.md)

## 里程碑进度

| 里程碑 | 内容 | 状态 |
| --- | --- | --- | --- |
| M1 | 领域层：聚合根、值对象、领域服务、仓储端口 | ✅ |
| M2 | 应用层与 Memory 仓储：IdempotencyService、command、DTO | ✅ |
| M3 | Redis 仓储：Lua 原子脚本、JSON record mapper | ✅ |
| M4 | HTTP 适配器：net/http、go-zero、Gin 中间件 | ✅ |
| M5 | gRPC 适配器：UnaryInterceptor、RPCCodec 端口与注册表 | ✅ |
| M6 | 可观测性：Logger/Metrics/Tracer 端口 | ✅ |
| + | 响应缓存策略：CaptureRules 领域服务 | ✅ |
| + | TTL 自动续期：Heartbeat 组件 | ✅ |
| P0 | 企业级配套：LICENSE、Dockerfile、docker-compose、CI/CD、健康检查、优雅关闭 | ✅ |
| P1 | 生产必备：YAML 配置加载、OTel 适配器、Redis 集成测试、Grafana Dashboard | ✅ |
| P2 | 增强竞争力：AES 加密、Pub/Sub 通知、Helm Chart、Swagger、Benchmark、多示例 | ✅ |
| P3 | 全面覆盖：SQL 持久化、Redis Sentinel、配置热加载、pprof 性能分析 | ✅ |
