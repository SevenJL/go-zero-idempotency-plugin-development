# Changelog

All notable changes to the go-zero idempotency plugin.

## [0.1.2] — 2026-06-04

### Fixed

- **P0 — Enterprise readiness**
  - Error wrapping: all 18 error return points in `IdempotencyService` now use `fmt.Errorf("%w")` preserving sentinel-error chains for `errors.Is()` checks
  - SQL `Abort` now includes `scope_service` in WHERE clause to prevent cross-scope interference matching the unique constraint
  - Test coverage expanded from 5 to 20 test cases covering disabled service, missing key, abort variants (delete/cache/default/keep_processing_ttl), complete edge cases (not found/owner mismatch/fingerprint mismatch/non-cacheable 5xx), WaitReplay timeout/complete-while-waiting, pass_through policy, multi-key isolation, and error wrapping verification

- **P1 — Production hardening**
  - `GoZeroLogger.Warn` no longer maps to `logx.Sloww` (slow-request channel); now maps to `Errorw` with explicit `level=warn` tag
  - `PubSubNotifier.Close()` now closes all subscriptions before returning; collects the first error instead of early-exit leaking remaining handles
  - `httpx.Middleware` heartbeat now uses `defer hb.Stop()` before `next.ServeHTTP()` to prevent goroutine leak on handler panic
  - SQL `Commit` now handles false-positive `ErrOwnerMismatch` when MySQL driver optimises no-op updates (RowsAffected=0 on identical values); re-reads record to distinguish idempotent retry from genuine mismatch
  - SQL `toDomain()` now returns deserialization errors from `json.Unmarshal` on `resp_headers` instead of silently swallowing corrupt data

- **P2 — Code quality**
  - Circuit breaker: added `trialActive atomic.Bool` with CAS gating in half-open state to prevent thundering-herd race where multiple goroutines raced through the trial gate
  - Lua scripts: replaced regex-based `string.find` JSON field extraction with robust `cjson.decode()` across all four atomic scripts (begin/commit/abort/renew), fixing edge cases with escaped quotes and nested objects
  - OTel metrics: documented `context.Background()` limitation in `CounterIncrement`/`HistogramObserve` (port interface doesn't accept context; noted for future revision)
  - `FilterHeaders` now always returns a deep copy including copied header-value slices
  - `IdempotencyService.Begin/Complete/Abort/WaitReplay` now have full GoDoc comments documenting all outcomes, error conditions, and design rationale
  - `NewProcessingRecord` uses local variable instead of mutating `params.Now`
  - `Scope` value object: fields unexported, added `NewScope()` constructor, `Service()/Tenant()/User()` getters, and `WithService()` immutable replacement; updated 13 files across all layers

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
  - Redis integration tests (10 scenarios, `//go:build integration`)
  - `deploy/grafana-dashboard.json` — Pre-built Grafana dashboard (10 panels)

- **P2 — Enhanced competitiveness**
  - `AESEncryptor` — AES-256-GCM body encryption for Redis at-rest data protection
  - `PubSubNotifier` — Redis Pub/Sub event notification (sub-ms WaitReplay)
  - `Notifier` port — pluggable notification interface (Redis, NATS, Kafka, etc.)
  - `deploy/helm/idempotency-example/` — Kubernetes Helm chart (Deployment, Service, HPA)
  - `deploy/swagger.json` — OpenAPI 3.0 API specification
  - `scripts/benchmark.sh` — Automated go-wrk benchmark suite (4 scenarios)
  - `scripts/run-tests.sh` — Test runner (unit, integration, Redis, coverage)
  - `examples/gozero-http/` — go-zero HTTP example with idempotency middleware
  - `examples/grpc/` — Native gRPC example with unary interceptor

- **P3 — Completeness**
  - `infrastructure/persistence/sql/` — MySQL/PostgreSQL repository with schema
  - `infrastructure/persistence/redis/sentinel.go` — Redis Sentinel HA support
  - `application/service/config_watcher.go` — YAML config hot-reload (5s polling)
  - pprof endpoints in Gin and go-zero-http examples (`/debug/pprof/`)

### Changed

- Gin example: `gin.New()` + `gin.Logger()` + `gin.Recovery()` replaces `gin.Default()`
- Gin example: added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to HTTP server
- go-zero-http example: added pprof and health check endpoints
- `.gitignore`: added coverage, Docker, OS entries
- Application layer: added `Notifier` port; rewired `IdempotencyService.WaitReplay` for Pub/Sub
- Redis repository: encryption support in `record_mapper.go` via `BodyEncryptor`

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
