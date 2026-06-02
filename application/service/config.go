package service

import (
	"time"

	"github.com/your-org/go-idempotency/application/port"
	"github.com/your-org/go-idempotency/domain/repository"
	domainservice "github.com/your-org/go-idempotency/domain/service"
)

type Config struct {
	// Disabled is an explicit opt-out. The service is enabled by default so an
	// omitted bool cannot accidentally turn idempotency into pass-through mode.
	Disabled bool
	Scope    string

	Repository repository.IdempotencyRecordRepository

	KeyResolver  port.KeyResolver
	Fingerprinter port.Fingerprinter
	OwnerFactory port.OwnerFactory
	Clock        port.Clock

	Policy domainservice.IdempotencyPolicy

	WaitTimeout  time.Duration
	WaitInterval time.Duration
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
	if c.WaitTimeout <= 0 {
		c.WaitTimeout = 5 * time.Second
	}
	if c.WaitInterval <= 0 {
		c.WaitInterval = 50 * time.Millisecond
	}
	return c
}
