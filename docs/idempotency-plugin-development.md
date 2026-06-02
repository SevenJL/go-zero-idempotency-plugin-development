# 基于 go-zero 的分布式通用幂等性插件开发文档

版本：v0.1  
日期：2026-06-02  
阶段：开发前设计文档

## 1. 背景与目标

在分布式服务中，同一个写请求可能因为客户端重试、网关超时、消息重复投递、网络抖动、服务重启等原因被执行多次。幂等性插件的目标是在业务代码之外提供一层通用保护，使同一个幂等请求在一段时间窗口内只产生一次业务副作用，并能在重复请求时返回一致结果或明确的冲突错误。

本项目从 0 设计一个以 go-zero 为主接入框架的分布式通用幂等性插件，同时提供 Gin、标准 net/http、原生 gRPC/go-zero zrpc 适配能力。整体设计采用“核心引擎 + 存储实现 + 框架适配器”的结构，避免幂等逻辑和具体 Web/RPC 框架耦合。

参考接入点：

- go-zero HTTP middleware：`server.Use()` 或 `.api` 文件按路由声明 middleware。
- go-zero gRPC/zrpc interceptor：`server.AddUnaryInterceptors(...)`。
- gRPC interceptor：适合承载跨 RPC 方法的通用行为。
- Gin middleware：`gin.HandlerFunc` 形式，通过 `c.Next()` 继续链路。
- go-zero Redis：可使用 `SetnxExCtx`、`NewRedisLock`、`ScriptRunCtx` 等 Redis 能力。

## 2. 术语

| 术语 | 说明 |
| --- | --- |
| Idempotency Key | 客户端或服务端生成的幂等键，用来标识一次业务意图。 |
| Fingerprint | 请求指纹，基于方法、路径/RPC 方法、租户、用户、请求体等内容计算，用于识别同一个 key 是否被不同请求复用。 |
| Processing | 首个请求已获得执行权，业务逻辑正在执行。 |
| Completed | 首个请求已执行完成，结果已缓存，可供重复请求重放。 |
| Failed | 业务执行失败后的状态，可按策略缓存或清理。 |
| Replay | 重复请求命中 Completed 后直接返回缓存结果。 |
| Conflict | 同一个 Idempotency Key 携带不同 Fingerprint，说明 key 被误用或攻击性复用。 |

## 3. 设计目标

1. 支持分布式部署，多实例共享幂等状态。
2. 默认基于 Redis，兼容 go-zero 内置 Redis client。
3. 支持 go-zero HTTP、go-zero zrpc/gRPC unary interceptor、Gin、net/http。
4. 核心引擎和框架无关，适配层尽量薄。
5. 支持重复请求返回缓存响应，包含 HTTP 状态码、响应头、响应体，RPC 场景支持响应对象序列化缓存。
6. 支持正在处理中的重复请求策略：立即拒绝、等待首个请求完成、返回自定义响应。
7. 支持请求指纹冲突检测。
8. 支持按路由、按方法、按业务操作配置 key 规则和策略。
9. 支持可观测性：日志、指标、trace 标签。
10. 支持失败策略可配置：失败不缓存、缓存失败结果、短 TTL 标记失败。

## 4. 非目标

1. 不承诺“全局 exactly once”。插件只能保证同一 Idempotency Key 在存储可用且 TTL 未过期窗口内的幂等保护。
2. 不替代数据库唯一键、事务、乐观锁等业务一致性手段。
3. 不默认对 GET/HEAD 等天然读请求启用。
4. 不默认支持 gRPC streaming 幂等保护。流式接口需要业务显式适配。
5. 不默认缓存超大响应体或文件下载响应。

## 5. DDD 总体架构

```text
client
  |
  v
interfaces layer
  - go-zero HTTP middleware
  - go-zero zrpc unary interceptor
  - Gin middleware
  - net/http middleware
  |
  v
application layer
  - StartIdempotency use case
  - CompleteIdempotency use case
  - ReplayIdempotency use case
  - WaitDuplicate use case
  |
  v
domain layer
  - IdempotencyRecord aggregate
  - IdempotencyKey/Fingerprint/Owner value objects
  - IdempotencyPolicy domain service
  - IdempotencyRecordRepository port
  ^
  |
infrastructure layer
  - RedisIdempotencyRecordRepository
  - MemoryIdempotencyRecordRepository
  - Codec/Serializer implementations
  - Logger/Metrics/Tracing implementations
```

依赖方向：

```text
interfaces -> application -> domain
infrastructure -> domain/application ports
```

DDD 分层职责：

| 分层 | 职责 | 不应该做的事 |
| --- | --- | --- |
| Domain | 表达幂等领域概念、状态机、策略和不变量 | 不依赖 go-zero/Gin/gRPC/Redis，不读 HTTP body |
| Application | 编排一次幂等用例，调用领域对象和仓储端口 | 不实现 Redis 细节，不直接写框架响应 |
| Infrastructure | 实现仓储、分布式原子操作、序列化、指标落地 | 不承载业务决策，不绕过领域状态机 |
| Interfaces | 适配 HTTP/RPC 框架，提取请求上下文，捕获和回放响应 | 不写 Redis，不复制状态机判断 |
```

核心原则：

- `Domain` 是插件最稳定的部分，只描述幂等业务语言，例如 key、指纹、占位、完成、冲突、重放。
- `Application` 暴露 `IdempotencyService`，框架适配器只能通过应用服务进入领域。
- `Infrastructure` 实现 `IdempotencyRecordRepository`，Redis 实现优先使用 Lua script，保证领域状态转移在分布式场景下仍具备原子性。
- `Interfaces` 只负责协议转换：把 go-zero/Gin/gRPC 请求转换为 `Command`，把 `Decision` 写回框架响应。
- 跨层传递使用命令、DTO 和领域对象，不把 `http.Request`、`gin.Context`、`grpc.UnaryServerInfo` 传入领域层。

## 6. DDD 包结构规划

```text
idempotency/
  domain/
    model/
      idempotency_record.go       # IdempotencyRecord 聚合根
      idempotency_status.go       # Processing/Completed/Failed
      idempotency_decision.go     # Acquired/Replay/Conflict/InProgress
      idempotency_error.go        # 领域错误
    valueobject/
      idempotency_key.go          # IdempotencyKey
      fingerprint.go              # Fingerprint
      owner.go                    # Owner
      operation.go                # Operation
      scope.go                    # Scope/Tenant/User
      ttl.go                      # ProcessingTTL/CompletedTTL/FailedTTL
    service/
      idempotency_policy.go       # DuplicatePolicy/FailureMode 判断
      fingerprint_policy.go       # 指纹冲突和范围规则
    repository/
      idempotency_record_repo.go  # 仓储端口
    event/
      events.go                   # Acquired/Replayed/Committed/Conflicted
  application/
    command/
      begin.go                    # BeginCommand
      complete.go                 # CompleteCommand
      abort.go                    # AbortCommand
      replay.go                   # ReplayCommand
    dto/
      request_context.go          # 框架无关请求上下文
      captured_response.go        # HTTP/RPC 响应快照 DTO
      result.go                   # BeginResult/CompleteResult
    service/
      idempotency_service.go      # 应用服务，编排 begin/complete/abort/wait
      wait_service.go             # duplicate wait 用例
    port/
      key_resolver.go             # KeyResolver
      fingerprinter.go            # Fingerprinter
      response_codec.go           # ResponseCodec/RPCCodec
      clock.go                    # Clock
      observability.go            # Logger/Metrics/Tracer 端口
    config/
      config.go                   # 应用配置
  infrastructure/
    persistence/
      redis/
        idempotency_record_repo.go # Redis 仓储实现
        record_mapper.go           # 领域对象 <-> Redis record
        scripts.go                 # Begin/Commit/Abort Lua
    memory/
        idempotency_record_repo.go # 单测和本地调试
    codec/
      json_response_codec.go
      proto_response_codec.go
    observability/
      gozero_logger.go
      noop_metrics.go
  interfaces/
    http/
      stdhttp/
        middleware.go             # net/http middleware
        response_writer.go
      gozero/
        middleware.go             # go-zero HTTP middleware
      gin/
        middleware.go             # Gin middleware
        response_writer.go
    rpc/
      grpc/
        unary_interceptor.go      # gRPC unary interceptor
      gozero/
        unary_interceptor.go      # go-zero zrpc 包装
  examples/
    gozerohttp/
    gozerorpc/
```

如果项目后续以 Go module 形式发布，建议模块名为：

```text
github.com/sevenjl/go-zero-idempotency-plugin-development
```

内部包名建议使用 `idempotency`，避免和 go-zero 框架强耦合。

### 6.1 聚合与边界

本插件只有一个核心聚合：`IdempotencyRecord`。它代表某个业务操作在一个 TTL 窗口内的幂等执行记录。

聚合内维护：

- `IdempotencyKey`
- `Fingerprint`
- `Owner`
- `Operation`
- `Status`
- `CapturedResponse`
- `CreatedAt/UpdatedAt/ExpiresAt`

聚合不变量：

1. `Processing` 状态才能 `Complete` 或 `Abort`。
2. `Complete` 必须校验 `Owner` 和 `Fingerprint`。
3. 同一个 `IdempotencyKey` 不能接受不同 `Fingerprint`。
4. `Completed` 状态不能被新的 processing 覆盖。
5. 响应快照必须满足缓存策略后才能进入 `Completed`。

### 6.2 仓储端口

仓储接口放在 `domain/repository`，由领域语言命名，不使用 Redis 或 Store 字样。

```go
type IdempotencyRecordRepository interface {
    TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error)
    Commit(ctx context.Context, record *model.IdempotencyRecord) error
    Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error
    Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error)
}
```

这里的 `TryBegin` 在 Redis 实现中会用 Lua 保证原子判断；在 Memory 实现中用 mutex 模拟相同语义。

### 6.3 应用服务

应用服务是框架入口唯一应该调用的对象。

```go
type IdempotencyService struct {
    repo IdempotencyRecordRepository
    keyResolver KeyResolver
    fingerprinter Fingerprinter
    policy *domainservice.IdempotencyPolicy
    clock Clock
}
```

核心方法：

```go
func (s *IdempotencyService) Begin(ctx context.Context, cmd command.BeginCommand) (dto.BeginResult, error)
func (s *IdempotencyService) Complete(ctx context.Context, cmd command.CompleteCommand) error
func (s *IdempotencyService) Abort(ctx context.Context, cmd command.AbortCommand) error
func (s *IdempotencyService) WaitReplay(ctx context.Context, cmd command.ReplayCommand) (dto.ReplayResult, error)
```

`IdempotencyService` 不是领域对象，它负责编排端口、事务边界和等待逻辑；真正的状态合法性由 `IdempotencyRecord` 和领域服务维护。

## 7. 核心状态机

### 7.1 状态定义

```text
missing
  |
  | Begin 成功
  v
processing
  |
  | Commit 成功
  v
completed

processing
  |
  | Abort / TTL 过期
  v
missing 或 failed
```

状态含义：

| 状态 | 含义 | 可见行为 |
| --- | --- | --- |
| missing | Redis 中没有该幂等记录 | 当前请求可尝试占位 |
| processing | 已有同 key 请求正在执行业务 | 按 duplicate policy 处理 |
| completed | 首次请求完成并缓存结果 | 重复请求直接 replay |
| failed | 首次请求失败并被缓存 | 按失败缓存策略 replay 或提示错误 |

### 7.2 状态字段

Redis value 建议使用 JSON 或 MessagePack。初版使用 JSON，便于排查。

```json
{
  "version": 1,
  "status": "processing",
  "key": "idem:order:create:tenant-1:01HX...",
  "fingerprint": "sha256:...",
  "owner": "instance-id/request-id",
  "created_at": 1717300000000,
  "updated_at": 1717300000000,
  "expires_at": 1717300600000,
  "response": {
    "status_code": 200,
    "headers": {
      "content-type": ["application/json"]
    },
    "body": "base64...",
    "body_encoding": "base64"
  },
  "rpc_response": {
    "codec": "json",
    "body": "base64..."
  },
  "error": {
    "code": "INTERNAL",
    "message": "..."
  }
}
```

### 7.3 TTL 设计

| TTL | 默认值 | 说明 |
| --- | --- | --- |
| ProcessingTTL | 30s | 防止业务执行中进程崩溃导致永久 processing。应大于接口 P99 超时。 |
| CompletedTTL | 24h | 成功结果缓存时间。 |
| FailedTTL | 5m | 失败结果缓存时间，仅在失败缓存启用时使用。 |
| WaitTimeout | 5s | 重复请求等待首个请求完成的最长时间。 |
| WaitInterval | 50ms | 轮询 completed 状态的间隔，可加 jitter。 |

注意：

- `ProcessingTTL` 必须小于或等于业务超时上限的合理倍数。过短可能导致首个请求未结束时第二个请求重新执行。
- 对高价值写操作，业务层仍应配合数据库唯一键或业务状态机。
- 响应缓存 TTL 不宜无限长，避免 Redis 成为历史结果存储。

## 8. Redis 仓储实现设计

### 8.1 Key 命名

推荐格式：

```text
{prefix}:{scope}:{operation}:{tenant}:{idempotencyKey}
```

示例：

```text
idem:http:POST:/api/orders:tenant-001:01HX9PKS...
idem:rpc:/order.OrderService/Create:tenant-001:01HX9PKS...
```

为兼容 Redis Cluster，后续可支持 hash tag：

```text
idem:{tenant-001}:http:POST:/api/orders:01HX9PKS...
```

### 8.2 Redis 仓储职责

Redis 实现位于 `infrastructure/persistence/redis`，实现领域层定义的 `IdempotencyRecordRepository`。它只负责持久化和分布式原子性，不负责决定“冲突时该返回 HTTP 409 还是 gRPC Aborted”。

`TryBegin` 需要原子完成以下判断：

1. key 不存在：写入 `processing`，返回 `Acquired`。
2. key 存在且 fingerprint 不一致：返回 `Conflict`。
3. key 存在且 status 为 `processing`：返回 `InProgress`。
4. key 存在且 status 为 `completed`：返回 `Replay`。
5. key 存在且 status 为 `failed`：按策略返回 `ReplayFailed` 或允许重试。

Redis value 和领域对象之间通过 `record_mapper.go` 转换。Redis record 可以是 JSON，但映射到领域层后必须恢复为 `IdempotencyRecord`、`IdempotencyKey`、`Fingerprint`、`Owner` 等强类型对象。

### 8.3 Redis 原子脚本

初版不建议只用 `SETNX + GET + SET` 多命令拼接，因为在状态判断和更新之间容易出现竞态。推荐 Redis Lua 脚本实现 `Begin` 和 `Commit`。

`Begin` 伪代码：

```lua
local existing = redis.call("GET", KEYS[1])
if not existing then
  redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
  return {"acquired"}
end

local record = cjson.decode(existing)
if record.fingerprint ~= ARGV[3] then
  return {"conflict", existing}
end

if record.status == "completed" then
  return {"replay", existing}
end

if record.status == "failed" then
  return {"failed", existing}
end

return {"in_progress", existing}
```

`Commit` 伪代码：

```lua
local existing = redis.call("GET", KEYS[1])
if not existing then
  return {"missing"}
end

local record = cjson.decode(existing)
if record.owner ~= ARGV[1] then
  return {"owner_mismatch"}
end
if record.fingerprint ~= ARGV[2] then
  return {"conflict"}
end
if record.status ~= "processing" then
  return {"invalid_state", record.status}
end

redis.call("SET", KEYS[1], ARGV[3], "EX", ARGV[4])
return {"committed"}
```

`Abort` 策略：

- `FailureModeDelete`：业务失败时删除 key，让下次请求可重新执行。
- `FailureModeCache`：写入 `failed`，缓存失败响应或错误。
- `FailureModeKeepProcessingUntilTTL`：不处理，让 processing 自然过期。仅用于极端场景，不推荐默认使用。

### 8.4 go-zero Redis 集成

Redis 仓储构造函数建议同时支持 go-zero Redis 和最小 Redis 接口：

```go
type RedisCmd interface {
    GetCtx(ctx context.Context, key string) (string, error)
    DelCtx(ctx context.Context, keys ...string) (int, error)
    ScriptRunCtx(ctx context.Context, script *redis.Script, keys []string, args ...any) (any, error)
}

func NewIdempotencyRecordRepository(rds *redis.Redis, opts ...RepositoryOption) repository.IdempotencyRecordRepository
```

如果 go-zero Redis 的脚本返回结构不满足需求，可在 Redis 仓储内部封装 `DoCtx` 执行 `EVAL`。优先沿用 go-zero 的 Redis client，以获得连接池、日志、指标和 trace 能力。

## 9. Key 生成与请求指纹

### 9.1 Key 来源优先级

默认顺序：

1. HTTP header：`Idempotency-Key`
2. gRPC metadata：`idempotency-key`
3. 请求字段：例如 JSON body 中的 `request_id`、`client_token`
4. 自定义 `KeyResolver`

生产环境建议要求客户端显式传入 key。服务端自动生成 key 只能用于内部链路，不适合保护客户端重试。

### 9.2 Key 约束

| 规则 | 推荐值 |
| --- | --- |
| 最小长度 | 16 |
| 最大长度 | 128 |
| 字符集 | `[A-Za-z0-9._:-]` |
| 是否允许空 key | 默认不允许 |
| 是否允许服务端自动生成 | 默认关闭 |

缺失 key 的行为：

- 默认：跳过幂等插件，继续业务逻辑。
- 严格模式：返回 400 或 gRPC InvalidArgument。
- 仅匹配写接口启用严格模式。

### 9.3 Fingerprint 组成

HTTP 默认指纹：

```text
sha256(
  tenant_id + "\n" +
  user_id + "\n" +
  http_method + "\n" +
  route_pattern + "\n" +
  canonical_body
)
```

gRPC 默认指纹：

```text
sha256(
  tenant_id + "\n" +
  user_id + "\n" +
  full_method + "\n" +
  canonical_proto_or_json_payload
)
```

注意：

- HTTP 路径优先使用 route pattern，例如 `/api/orders/:id`，避免 path 参数造成意外不一致。
- JSON body 需要规范化，避免字段顺序导致指纹不同。
- 文件上传、流式 body、大 body 默认不参与完整缓存，可改用业务字段或 body hash。
- 指纹中必须包含租户/用户/业务 scope，避免不同主体复用同一个 key。

## 10. 核心流程

### 10.1 首次请求

```text
interfaces extracts protocol context
  -> application builds BeginCommand
  -> application resolves IdempotencyKey and Fingerprint
  -> domain creates IdempotencyRecord(processing)
  -> repository.TryBegin returns Acquired
  -> interfaces executes business handler
  -> interfaces captures response as DTO
  -> application calls domain Complete/Abort
  -> repository.Commit or repository.Abort
  -> interfaces returns original response
```

### 10.2 重复请求命中 Completed

```text
interfaces extracts protocol context
  -> application builds BeginCommand
  -> application resolves key and fingerprint
  -> repository.TryBegin returns Replay with IdempotencyRecord
  -> application converts record to ReplayResult
  -> interfaces writes cached response
  -> business handler is not executed
```

### 10.3 重复请求命中 Processing

策略由领域服务 `IdempotencyPolicy` 和应用层等待用例共同控制：

| 策略 | 行为 | 适用场景 |
| --- | --- | --- |
| Reject | 立即返回 409/TooManyRequests 或 gRPC Aborted | 默认策略，简单安全 |
| Wait | 轮询等待 completed，成功后 replay，超时后返回 in-progress | 客户端重试频繁且业务耗时短 |
| PassThrough | 不拦截，继续执行业务 | 仅用于调试，不建议生产使用 |
| Custom | 调用自定义回调 | 需要特殊错误码或响应结构 |

### 10.4 Fingerprint 冲突

同一个 Idempotency Key 但指纹不同：

- HTTP 返回 409 Conflict。
- gRPC 返回 `codes.Aborted` 或 `codes.InvalidArgument`，默认推荐 `Aborted`。
- 日志记录 key、operation、tenant、old/new fingerprint 前缀，不记录敏感 payload。
- 冲突判断属于领域规则，HTTP/gRPC 错误码映射属于接口层职责。

## 11. HTTP 响应捕获与 Replay

### 11.1 CapturedResponse

```go
type CapturedResponse struct {
    StatusCode int
    Headers    http.Header
    Body       []byte
}
```

### 11.2 捕获规则

1. 只缓存白名单 content-type，例如 `application/json`、`text/plain`。
2. 默认最大响应体大小 1MB，超过则不缓存 body，并按策略报错或降级为不幂等缓存。
3. 不缓存 `Set-Cookie`、`Authorization`、`Cookie` 等敏感头。
4. 可配置额外排除头，例如 `X-Request-Id`、trace header。
5. replay 时建议增加响应头：

```text
Idempotency-Replayed: true
Idempotency-Key: <key>
```

### 11.3 状态码策略

| 首次响应 | 默认是否缓存 | 说明 |
| --- | --- | --- |
| 2xx | 是 | 成功响应 |
| 3xx | 否 | 重定向通常和上下文有关 |
| 4xx | 可配置 | 默认不缓存，业务可打开 |
| 5xx | 否 | 默认失败后删除 processing |

## 12. gRPC 响应捕获与 Replay

### 12.1 Unary RPC

只默认支持 unary RPC。核心 interceptor 形态：

```go
func UnaryServerInterceptor(svc *appservice.IdempotencyService, opts ...Option) grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
        // interface -> application begin -> handler -> application complete/abort/replay
    }
}
```

### 12.2 RPC 响应序列化

推荐序列化顺序：

1. 如果响应实现 `proto.Message`，使用 protobuf binary。
2. 否则使用 JSON。
3. 可配置自定义 `RPCCodec`。

`Replay` 时必须能够恢复为 RPC handler 的响应类型。因为拦截器拿不到所有业务类型注册表，建议提供以下两种方式：

- `ResponseCloner`：从当前 method 获取响应类型构造函数。
- `CodecRegistry`：按 `info.FullMethod` 注册响应解码器。

示例：

```go
codecRegistry.Register("/order.OrderService/Create", func() any {
    return &orderpb.CreateOrderResp{}
})
```

### 12.3 gRPC 错误策略

| 场景 | 默认 code |
| --- | --- |
| 缺失 key 且严格模式开启 | `InvalidArgument` |
| key 冲突 | `Aborted` |
| 正在处理且 Reject | `Aborted` |
| 等待超时 | `DeadlineExceeded` |
| Redis 不可用且 FailClosed | `Unavailable` |
| Redis 不可用且 FailOpen | 继续执行业务 |

## 13. go-zero HTTP 适配设计

### 13.1 使用方式

全局 middleware：

```go
rds := redis.MustNewRedis(c.Redis)
repo := redisrepo.NewIdempotencyRecordRepository(rds)
idemSvc := appservice.NewIdempotencyService(appservice.Config{
    Repository: repo,
    Scope: "order-api",
})

server := rest.MustNewServer(c.RestConf)
server.Use(gozerohttp.NewMiddleware(idemSvc))
```

按路由 middleware：

```api
@server(
    middleware: Idempotency
)
service order-api {
    @handler CreateOrder
    post /api/orders (CreateOrderReq) returns (CreateOrderResp)
}
```

在 `ServiceContext` 中注入：

```go
type ServiceContext struct {
    Config      config.Config
    Idempotency rest.Middleware
    IdemSvc     *appservice.IdempotencyService
}
```

### 13.2 路由级配置

go-zero 生成的 middleware 默认只能拿到 `http.Request`。如果需要按 handler 设置策略，可提供 wrapper：

```go
func WithRoutePolicy(policy domainservice.IdempotencyPolicy) rest.Middleware
```

或者通过配置表按 method + path 匹配：

```yaml
Idempotency:
  Enabled: true
  Routes:
    - Method: POST
      Path: /api/orders
      Required: true
      CompletedTTL: 24h
      DuplicatePolicy: wait
```

## 14. Gin 适配设计

### 14.1 使用方式

```go
idemSvc := appservice.NewIdempotencyService(...)
r := gin.New()
r.Use(ginidem.New(idemSvc))

r.POST("/api/orders", createOrder)
```

### 14.2 Gin 注意点

- 读取 body 计算指纹后，需要把 body 放回 `c.Request.Body`，避免业务 handler 无法再次读取。
- response writer 需要包装 `gin.ResponseWriter`，捕获 status、header、body。
- replay 后调用 `c.Abort()` 阻止后续 handler 执行。

## 15. net/http 适配设计

标准 middleware 用于兼容其他框架：

```go
func Middleware(svc *appservice.IdempotencyService) func(http.Handler) http.Handler
```

go-zero HTTP 适配器也可复用该实现，只是类型包装为 `rest.Middleware`。

## 16. 配置设计

配置可以从同一份 YAML 读取，但在代码中应拆分到不同层使用：

| 配置归属 | 示例字段 | 使用位置 |
| --- | --- | --- |
| Domain Policy | `DuplicatePolicy`、`FailureMode`、TTL 规则 | `domain/service` |
| Application | `WaitTimeout`、`WaitInterval`、`StorageFailureMode` | `application/service` |
| Interfaces | HTTP header、metadata、body 读取上限、响应捕获规则 | `interfaces/http`、`interfaces/rpc` |
| Infrastructure | Redis key prefix、script timeout、codec | `infrastructure/persistence/redis` |

应用启动时由 composition root 负责把总配置拆成各层配置。领域层不读取 YAML，也不感知 go-zero 的 `Config` 结构。
外部 YAML 可以继续使用 `Enabled` 便于业务配置表达；组装应用层 `Config` 时建议转换为零值启用的 `Disabled`，避免 Go bool 默认值把插件静默关闭。

### 16.1 Go Config

```go
type Config struct {
    Disabled bool
    Scope string

    Key KeyConfig
    Fingerprint FingerprintConfig

    ProcessingTTL time.Duration
    CompletedTTL time.Duration
    FailedTTL time.Duration

    DuplicatePolicy DuplicatePolicy
    FailureMode FailureMode
    StorageFailureMode StorageFailureMode

    WaitTimeout time.Duration
    WaitInterval time.Duration

    Capture CaptureConfig
    Observability ObservabilityConfig
}
```

### 16.2 YAML 示例

```yaml
Idempotency:
  Enabled: true
  Scope: order-api
  Key:
    HeaderName: Idempotency-Key
    Required: true
    MinLength: 16
    MaxLength: 128
  Fingerprint:
    IncludeTenant: true
    IncludeUser: true
    IncludeBody: true
    MaxBodyBytes: 1048576
  ProcessingTTL: 30s
  CompletedTTL: 24h
  FailedTTL: 5m
  DuplicatePolicy: wait
  FailureMode: delete
  StorageFailureMode: fail_closed
  WaitTimeout: 5s
  WaitInterval: 50ms
  Capture:
    MaxBodyBytes: 1048576
    ContentTypes:
      - application/json
    ExcludedHeaders:
      - Set-Cookie
      - Authorization
      - Cookie
      - X-Request-Id
```

## 17. 可观测性

### 17.1 日志

核心日志字段：

| 字段 | 说明 |
| --- | --- |
| `idempotency.key_hash` | key 的 hash，避免明文泄露 |
| `idempotency.status` | acquired/replay/conflict/in_progress |
| `idempotency.operation` | HTTP route 或 RPC full method |
| `idempotency.tenant` | 租户 ID |
| `idempotency.owner` | 当前请求 owner |
| `idempotency.duration_ms` | 幂等处理耗时 |

### 17.2 指标

建议暴露：

```text
idempotency_begin_total{result="acquired|replay|conflict|in_progress|error"}
idempotency_commit_total{result="success|error|owner_mismatch"}
idempotency_wait_seconds_bucket
idempotency_replay_total
idempotency_storage_errors_total
idempotency_record_bytes_bucket
```

### 17.3 Trace

在当前 span 上设置 tag：

```text
idempotency.key_hash
idempotency.result
idempotency.replayed
```

## 18. 安全与合规

1. 幂等 key 不直接写入日志，使用 hash 或前后截断。
2. 指纹不记录原始请求体。
3. 响应缓存默认不保存敏感响应头。
4. 对包含隐私数据的响应，业务可关闭 response replay，仅做执行去重。
5. Redis key 应包含租户/用户 scope，避免跨租户碰撞。
6. key 长度和字符集必须校验，防止超长 key 攻击和 Redis 内存滥用。
7. 响应体大小必须限制。

## 19. 容错策略

### 19.1 Redis 不可用

| 模式 | 行为 | 适用场景 |
| --- | --- | --- |
| FailClosed | 返回错误，不执行业务 | 金融、订单、支付等强幂等场景 |
| FailOpen | 记录日志并继续业务 | 对可用性要求高，业务自身有兜底 |

默认推荐 `FailClosed`，但非核心接口可配置 `FailOpen`。

### 19.2 首个请求崩溃

如果首个请求在业务执行中崩溃：

- Redis 中保持 `processing` 到 `ProcessingTTL` 过期。
- TTL 过期后新请求可以重新执行。
- 若业务副作用已经发生但未 commit，仍可能重复执行，因此业务层必须用数据库唯一键或业务状态保护关键副作用。

### 19.3 Commit 失败

业务成功但 commit Redis 失败是最危险场景。默认处理：

1. 记录 error 日志和指标。
2. 返回业务原始响应，但标记 `Idempotency-Commit-Failed: true` 可选。
3. 强业务场景可配置为 commit 失败时返回 500，但这可能导致客户端重试和业务重复，需要按业务取舍。

## 20. 测试计划

### 20.1 单元测试

- Domain 聚合：`IdempotencyRecord` 的 begin、complete、abort、owner mismatch、fingerprint conflict。
- Value Object：`IdempotencyKey` 长度/字符集、`Fingerprint` 规范化、`Owner` 唯一性。
- Domain Service：`DuplicatePolicy`、`FailureMode`、TTL 规则。
- Application Service：begin/replay/wait/complete/abort 编排。
- KeyResolver：header、metadata、body field、自定义 resolver。
- Fingerprinter：JSON 字段顺序、空 body、大 body、租户/用户维度。
- Interface response capture：状态码、header 过滤、body size limit。
- RPC codec：proto/json encode/decode。

### 20.2 Redis 集成测试

- 并发 100 个同 key 请求，只允许一个 acquired。
- 同 key 不同 body 返回 conflict。
- processing TTL 过期后可重新 acquired。
- completed TTL 过期后可重新 acquired。
- commit owner mismatch 不允许覆盖。
- Redis Cluster hash tag key 测试。

### 20.3 框架适配测试

- go-zero HTTP middleware：POST 首次执行，重复 replay。
- go-zero route middleware：只对声明路由启用。
- Gin middleware：`c.Abort()` 后业务 handler 不执行。
- net/http middleware：标准 handler 可复用。
- gRPC unary interceptor：重复请求返回缓存 response。

### 20.4 压测

关注：

- Redis QPS。
- Lua 脚本平均和 P99 耗时。
- response body capture 内存分配。
- wait 策略在高并发重复请求下的连接占用。

## 21. 开发里程碑

### M1：领域层

- 完成 `IdempotencyRecord` 聚合根。
- 完成 `IdempotencyKey`、`Fingerprint`、`Owner`、`Operation` 等值对象。
- 完成 `IdempotencyPolicy` 领域服务。
- 完成 `IdempotencyRecordRepository` 仓储端口。
- 单元测试覆盖领域状态机和不变量。

### M2：应用层和 Memory 仓储

- 完成 `IdempotencyService`。
- 完成 command、DTO、port。
- 完成 `MemoryIdempotencyRecordRepository`。
- 完成应用服务单元测试。

### M3：Redis 仓储

- 完成 Begin/Commit/Abort Lua 脚本。
- 接入 go-zero Redis。
- 完成 Redis 集成测试。

### M4：HTTP 接口层适配器

- 完成 net/http middleware。
- 完成 go-zero HTTP middleware。
- 完成 Gin middleware。
- 完成响应捕获和 replay。

### M5：gRPC/go-zero zrpc 接口层适配器

- 完成 unary interceptor。
- 完成 RPC codec registry。
- 完成 go-zero zrpc 示例。

### M6：可观测性和示例项目

- 接入日志、指标、trace hook。
- 提供 go-zero API 示例。
- 提供 go-zero RPC 示例。
- 编写 README 和使用指南。

## 22. 初版 API 草案

### 22.1 Composition Root

```go
repo := redisrepo.NewIdempotencyRecordRepository(rds, redisrepo.Options{
    KeyPrefix: "idem",
})

idemSvc := appservice.NewIdempotencyService(appservice.Config{
    Scope: "order-api",
    Repository: repo,
    Key: idempotency.KeyConfig{
        HeaderName: "Idempotency-Key",
        Required: true,
    },
    DuplicatePolicy: idempotency.DuplicateWait,
})
```

### 22.2 go-zero HTTP

```go
server.Use(gozerohttp.NewMiddleware(idemSvc))
```

### 22.3 Gin

```go
r.Use(ginidem.New(idemSvc))
```

### 22.4 gRPC/zrpc

```go
server.AddUnaryInterceptors(gozerorpc.UnaryServerInterceptor(idemSvc))
```

## 23. 关键实现细节

### 23.1 Owner

每个 acquired 请求生成唯一 owner：

```text
<instance-id>/<request-id>/<monotonic-counter>
```

Commit 和 Abort 必须校验 owner，避免 processing TTL 即将过期时，旧请求覆盖新请求结果。

### 23.2 Body 读取

HTTP 适配器读取 body 后必须恢复：

```go
body, _ := io.ReadAll(req.Body)
req.Body = io.NopCloser(bytes.NewReader(body))
```

大 body 可只 hash 前 N bytes 或禁用 body fingerprint，但必须在文档和配置中明确。

### 23.3 Replay Header

Replay 时添加：

```text
Idempotency-Replayed: true
```

可选添加：

```text
Idempotency-Status: replayed
```

### 23.4 Wait 策略

wait 策略初版用短轮询实现：

```text
while now < deadline:
  record = store.Get(key)
  if record completed: replay
  if record missing: retry Begin
  sleep(interval + jitter)
return in_progress timeout
```

后续可扩展 Redis Pub/Sub 或 Stream 事件通知，减少轮询。

## 24. 风险与权衡

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 业务成功但 commit 失败 | 重试可能重复执行业务 | 业务唯一约束、commit 重试、强场景 FailClosed |
| ProcessingTTL 设置过短 | 并发重复执行 | TTL 大于业务超时，owner 校验 |
| 响应缓存过大 | Redis 内存压力 | body size limit、content-type 白名单 |
| key 被跨用户复用 | 数据泄露或冲突 | fingerprint 加 tenant/user，key 日志脱敏 |
| gRPC 响应类型无法反序列化 | replay 失败 | method response registry |
| Redis 单点故障 | 幂等失效或接口不可用 | Redis 高可用、FailOpen/FailClosed 配置 |

## 25. 推荐默认值

```yaml
Enabled: true
Key.Required: true
Key.HeaderName: Idempotency-Key
ProcessingTTL: 30s
CompletedTTL: 24h
FailedTTL: 5m
DuplicatePolicy: reject
FailureMode: delete
StorageFailureMode: fail_closed
Capture.MaxBodyBytes: 1048576
Capture.ContentTypes:
  - application/json
```

默认只对以下 HTTP 方法启用：

```text
POST, PUT, PATCH, DELETE
```

默认跳过：

```text
GET, HEAD, OPTIONS
```

## 26. 后续实现顺序建议

1. 先实现 `domain`：聚合根、值对象、领域服务、仓储端口，确保状态机和不变量可靠。
2. 再实现 `application`：`IdempotencyService`、command、DTO、等待策略、端口编排。
3. 然后实现 `infrastructure/persistence/memory`，用单元测试验证应用服务。
4. 再实现 `infrastructure/persistence/redis`，用并发测试验证 Lua 原子性。
5. 接着实现 `interfaces/http/stdhttp` 和 `interfaces/http/gozero`，快速形成可运行示例。
6. 再补 `interfaces/http/gin`、`interfaces/rpc/grpc`、`interfaces/rpc/gozero`。
7. 示例项目、可观测性和压测放在核心功能稳定后补齐。

## 27. 官方资料

- go-zero HTTP Middleware：<https://go-zero.dev/guides/http/server/middleware/>
- go-zero gRPC Interceptors：<https://go-zero.dev/guides/grpc/interceptor/>
- go-zero Redis：<https://go-zero.dev/guides/database/redis/>
- go-zero Redis package：<https://pkg.go.dev/github.com/zeromicro/go-zero/core/stores/redis>
- gRPC Interceptors：<https://grpc.io/docs/guides/interceptors/>
- Gin Middleware：<https://gin-gonic.com/en/docs/middleware/>
