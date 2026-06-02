package service

import (
	"time"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/port"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/repository"
	domainservice "github.com/SevenJL/go-zero-idempotency-plugin-development/domain/service"
)

type Config struct {
	// Disabled is an explicit opt-out. The service is enabled by default so an
	// omitted bool cannot accidentally turn idempotency into pass-through mode.
	Disabled bool
	Scope    string

	Repository repository.IdempotencyRecordRepository

	KeyResolver   port.KeyResolver
	Fingerprinter port.Fingerprinter
	OwnerFactory  port.OwnerFactory
	Clock         port.Clock

	Policy domainservice.IdempotencyPolicy

	WaitTimeout  time.Duration
	WaitInterval time.Duration

	// CaptureRules controls which responses are cached for replay.
	// Zero value means safe defaults (cache 2xx, 1 MB limit, JSON only).
	CaptureRules domainservice.CaptureRules

	// Logger receives structured log events. Defaults to no-op.
	Logger port.Logger

	// Metrics receives counter and histogram observations. Defaults to no-op.
	Metrics port.Metrics

	// Tracer creates spans for distributed tracing. Defaults to no-op.
	Tracer port.Tracer
}

func (c Config) normalized() Config {
	if c.KeyResolver == nil {
		c.KeyResolver = HeaderKeyResolver{Required: true}
	}
	if c.Fingerprinter == nil {
		c.Fingerprinter = SHA256Fingerprinter{}
	}
	if c.OwnerFactory == nil {
		c.OwnerFactory = RandomOwnerFactory{}
	}
	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	c.Policy = domainservice.NewIdempotencyPolicy(c.Policy.DuplicatePolicy, c.Policy.TTL)
	c.CaptureRules = domainservice.NewCaptureRules(c.CaptureRules)
	if c.WaitTimeout <= 0 {
		c.WaitTimeout = 5 * time.Second
	}
	if c.WaitInterval <= 0 {
		c.WaitInterval = 50 * time.Millisecond
	}
	if c.Logger == nil {
		c.Logger = port.NoopLogger()
	}
	if c.Metrics == nil {
		c.Metrics = port.NoopMetrics()
	}
	if c.Tracer == nil {
		c.Tracer = port.NoopTracer()
	}
	return c
}
