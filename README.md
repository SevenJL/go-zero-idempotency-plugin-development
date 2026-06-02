# go-zero 分布式通用幂等性插件

当前仓库处于开发前设计阶段，已先完成按 DDD 领域分层设计的插件开发文档。

当前代码进度：

- 已初始化 Go module。
- 已完成领域层初版：聚合根、值对象、领域策略、仓储端口。
- 已完成应用层初版：`IdempotencyService`、command、DTO、默认 key resolver/fingerprinter。
- 已完成 Memory 仓储初版和基础单元测试。
- 已完成一次 code review 修正：默认零值启用、TTL 校验、wait 失败状态处理、Memory 仓储提交校验。

当前目录：

- `domain/`：幂等领域模型、值对象、领域策略、仓储端口。
- `application/`：幂等应用服务、command、DTO、端口和默认实现。
- `infrastructure/persistence/memory/`：用于单测和本地调试的内存仓储。
- `docs/`：DDD 设计和开发文档。

验证说明：

- 当前环境未找到 `go` / `gofmt` 命令，尚未执行 `gofmt` 和 `go test ./...`。
- 安装或配置 Go 后，应先运行 `gofmt -w application domain infrastructure`，再运行 `go test ./...`。

文档入口：

- [基于 go-zero 的分布式通用幂等性插件开发文档](docs/idempotency-plugin-development.md)

后续建议按文档中的里程碑推进：

1. 领域层：聚合根、值对象、领域服务、仓储端口
2. 应用层：幂等应用服务、command、DTO、等待策略
3. 基础设施层：Memory/Redis 仓储实现
4. 接口层：go-zero HTTP、Gin、net/http、gRPC/zrpc 适配器
5. 示例项目、可观测性和压测
