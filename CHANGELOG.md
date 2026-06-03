# Changelog

All notable changes to the go-zero idempotency plugin.

## [0.2.0] — 2026-06-03

### Added

- **P0 — Enterprise readiness**
  - `LICENSE` — MIT license
  - `Dockerfile` — Multi-stage build (golang:1.25-alpine → scratch)
  - `docker-compose.yml` — App + Redis one-command development environment
  - `.github/workflows/ci.yml` — CI pipeline (lint → test → build → docker smoke)
  - Health & readiness endpoints (`/health`, `/ready`) in Gin example
  - Graceful shutdown (SIGINT/SIGTERM, 30s timeout) in Gin example

- **P1 — Production essentials**
  - `ConfigFile` — YAML-deserializable configuration with safe defaults
  - `OTelLogger` — OpenTelemetry slog-based logger adapter
  - `OTelMetrics` — OpenTelemetry metrics adapter (counters + histograms)
  - `OTelTracer` — OpenTelemetry tracing adapter (span creation + attributes)
  - Redis integration tests (12 scenarios, `//go:build integration`)
  - `deploy/grafana-dashboard.json` — Pre-built Grafana dashboard (10 panels)

### Changed

- Gin example: `gin.New()` + `gin.Logger()` + `gin.Recovery()` replaces `gin.Default()`
- Gin example: added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to HTTP server
- `.gitignore`: added coverage, Docker, OS entries

## [0.1.0] — 2026-06-02

### Added

- **M1** — Domain layer: `IdempotencyRecord` aggregate, value objects, domain services, repository port
- **M2** — Application layer: `IdempotencyService`, CQRS commands, DTOs, default port implementations, Memory repository
- **M3** — Redis repository: Lua atomic scripts (Begin/Commit/Abort/Renew), JSON record mapper, circuit breaker, Redis Cluster hash tag support
- **M4** — HTTP adapters: net/http, go-zero, Gin middleware with response capture & replay
- **M5** — gRPC adapter: `UnaryServerInterceptor`, `RPCCodec` port & registry
- **M6** — Observability: Logger/Metrics/Tracer ports with go-zero implementations
- Response caching: `CaptureRules` domain service (status code / content-type / body size rules)
- Heartbeat: automatic TTL renewal for long-running handlers
- 31+ test cases across unit, integration, and HTTP integration tests
- Gin example application with bilingual test UI
- Performance benchmark report (go-wrk, MacBook Air M4, ~55K QPS)
- Comprehensive DDD design documentation
