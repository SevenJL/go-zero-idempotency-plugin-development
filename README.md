# go-zero 分布式通用幂等性插件

当前仓库处于开发前设计阶段，已先完成按 DDD 领域分层设计的插件开发文档。

文档入口：

- [基于 go-zero 的分布式通用幂等性插件开发文档](docs/idempotency-plugin-development.md)

后续建议按文档中的里程碑推进：

1. 领域层：聚合根、值对象、领域服务、仓储端口
2. 应用层：幂等应用服务、command、DTO、等待策略
3. 基础设施层：Memory/Redis 仓储实现
4. 接口层：go-zero HTTP、Gin、net/http、gRPC/zrpc 适配器
5. 示例项目、可观测性和压测
