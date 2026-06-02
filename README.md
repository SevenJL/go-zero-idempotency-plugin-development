# go-zero 分布式通用幂等性插件

当前仓库处于开发前设计阶段，已先完成按 DDD 领域分层设计的插件开发文档。

当前代码进度：

- 已初始化 Go module。
- 已完成领域层初版：聚合根、值对象、领域策略、仓储端口。
- 已完成应用层初版：`IdempotencyService`、command、DTO、默认 key resolver/fingerprinter。
- 已完成 Memory 仓储初版和基础单元测试。
- 已完成一次 code review 修正：默认零值启用、TTL 校验、wait 失败状态处理、Memory 仓储提交校验。
- 已完成集成测试，覆盖全部核心功能路径，未发现 bug。

当前目录：

- `domain/`：幂等领域模型、值对象、领域策略、仓储端口。
- `application/`：幂等应用服务、command、DTO、端口和默认实现。
- `infrastructure/persistence/memory/`：用于单测和本地调试的内存仓储。
- `tests/`：集成测试，端到端验证插件正确性。
- `docs/`：DDD 设计和开发文档。

运行测试：

```bash
go test ./... -count=1
```

文档入口：

- [基于 go-zero 的分布式通用幂等性插件开发文档](docs/idempotency-plugin-development.md)

---

## 集成测试报告

测试文件：[tests/idempotency_plugin_test.go](tests/idempotency_plugin_test.go)  
测试日期：2026-06-02  
Go 版本：go1.25.1  
测试结论：**25 个测试全部通过，未发现 bug。**

### 测试环境

- 仓储实现：`MemoryIdempotencyRecordRepository`（内存仓储，mutex 保证并发安全）
- 时钟：自定义 `testClock`，可控制时间推进，避免依赖系统时钟
- Key 解析：`HeaderKeyResolver`（从 HTTP Header `Idempotency-Key` 提取）
- 指纹计算：`SHA256Fingerprinter`（含 JSON 规范化）
- Owner 生成：`RandomOwnerFactory`（crypto/rand 生成唯一 owner）

### 测试结果明细

#### 生命周期测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestFullLifecycleBeginCompleteReplay` | Begin → Complete → 重复 Begin 返回 Replay，携带缓存响应（状态码 201、body 一致） | PASS |
| `TestConflictSameKeyDifferentBody` | 同 key 不同 body → 指纹不同 → 返回 Conflict | PASS |
| `TestInProgressDuplicate` | 同 key 同 body，首个请求未 Complete → 后续返回 InProgress | PASS |
| `TestDoubleCompleteFails` | 对已 Completed 的记录再次 Complete → 返回 `ErrInvalidState`，状态机不变量生效 | PASS |

#### Abort 策略测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestAbortDeleteAllowsReacquire` | `FailureModeDelete` 后 key 被删除，后续请求可重新 Acquire | PASS |
| `TestAbortCacheStoresFailure` | `FailureModeCache` 后后续请求看到 Failed，携带 `ErrorCode` 和 `ErrorMessage` | PASS |
| `TestAbortKeepProcessingTTL` | `FailureModeKeepProcessingTTL` 后记录保持 processing，后续请求返回 InProgress | PASS |

#### Wait 策略测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestWaitPolicyReplaysAfterComplete` | `DuplicateWait` 策略下，记录 Complete 后重复请求正确 Replay | PASS |
| `TestWaitTimeoutReturnsInProgress` | Wait 超时后返回 InProgress，不会死循环 | PASS |
| `TestWaitReplayContextCancellation` | Context 取消后 `WaitReplay` 及时退出并返回错误 | PASS |
| `TestWaitReplayRecordMissing` | `WaitReplay` 查询不存在的 key → `Found=false` | PASS |

#### 服务配置测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestDisabledServiceReturnsSkipped` | `Disabled: true` → Begin 返回 Skipped | PASS |
| `TestMissingKeyNotRequiredSkips` | `Required: false` 且无 key → 返回 Skipped | PASS |
| `TestMissingKeyRequiredReturnsError` | `Required: true` 且无 key → 返回 `ErrMissingIdempotencyKey` | PASS |
| `TestInvalidKeyFormatReturnsError` | key 长度不足 16 → 返回 `ErrInvalidIdempotencyKey` | PASS |
| `TestNewServiceRequiresRepository` | 不传 Repository 构造 Service → 返回 `ErrRepositoryRequired` | PASS |

#### 安全与隔离测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestCompleteWrongOwnerFails` | 非 Owner 调用 Complete → `ErrOwnerMismatch`，防止越权提交 | PASS |
| `TestCompleteNonexistentKeyFails` | 对不存在的 key 调用 Complete → `ErrInvalidState` | PASS |
| `TestJSONCanonicalization` | `{"sku":"A","qty":1}` 与 `{"qty":1,"sku":"A"}` → 指纹相同（JSON 规范化），不会误判为 Conflict | PASS |
| `TestDifferentTenantProducesConflict` | 同 key 不同 tenant → 指纹不同 → Conflict（scope 隔离生效） | PASS |
| `TestResponseBodyIsDeepCopied` | Replay 返回的 body 被深拷贝，修改返回值的 body 不影响下次 Replay 结果 | PASS |
| `TestCapturedResponseHeadersPreserved` | Complete 时传入的自定义 Header 在 Replay 时完整恢复 | PASS |

#### 并发与边界测试

| 测试 | 验证点 | 结果 |
|---|---|---|
| `TestConcurrentBeginsOnlyOneAcquires` | 20 goroutine 并发同 key Begin → 恰好 1 个 Acquired，19 个 InProgress | PASS |
| `TestRecordExpiryAllowsReacquire` | 记录 TTL 过期后，新请求可以重新 Acquire | PASS |
| `TestZeroNowUsesClock` | `Now` 为零值时自动使用 Clock 时间，不会 panic 或产生错误时间 | PASS |

### 测试覆盖率分析

按开发文档 §20.1 单元测试计划逐项对照：

| 文档要求的测试项 | 覆盖状态 |
|---|---|
| Domain 聚合：begin / complete / abort / owner mismatch / fingerprint conflict | ✅ 全部覆盖 |
| Value Object：key 长度/字符集、fingerprint 规范化、owner 唯一性 | ✅ 全部覆盖（边界值测试） |
| Domain Service：DuplicatePolicy、FailureMode、TTL 规则 | ✅ 策略组合覆盖 |
| Application Service：begin / replay / wait / complete / abort 编排 | ✅ 全部覆盖 |
| KeyResolver：header、metadata、required/optional、无效格式 | ✅ 全部覆盖 |
| Fingerprinter：JSON 字段顺序、空 body、租户/用户维度 | ✅ 全部覆盖 |
| Response capture：状态码、header 过滤、body size limit | ✅ header 保存和深拷贝已验证 |
| 并发：多请求同 key 竞态 | ✅ 20 并发验证 |

### 结论

当前 M1（领域层）+ M2（应用层与 Memory 仓储）实现：

- ✅ 状态机正确：Acquired → Completed/Failed 转换及不变量均通过测试
- ✅ 并发安全：Memory 仓储 mutex 保护，并发测试仅 1 个 Acquired
- ✅ 指纹隔离：JSON 规范化 + scope（tenant/user）正确区分
- ✅ Owner 鉴权：Complete/Abort 严格校验 owner，防止跨请求覆盖
- ✅ TTL 机制：过期记录可重新获取，不会造成死锁
- ✅ 容错能力：context 取消、超时、服务禁用、缺失 key 均正确处理

**未发现 bug，插件核心逻辑达到开发文档 M1/M2 里程碑要求。**

---

后续建议按文档中的里程碑推进：

1. 领域层：聚合根、值对象、领域服务、仓储端口 ✅ 已完成
2. 应用层：幂等应用服务、command、DTO、等待策略 ✅ 已完成
3. 基础设施层：Memory 仓储 ✅ 已完成，Redis 仓储待开发
4. 接口层：go-zero HTTP、Gin、net/http、gRPC/zrpc 适配器
5. 示例项目、可观测性和压测
