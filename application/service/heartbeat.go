package service

import (
	"context"
	"log"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/repository"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

// Heartbeat periodically extends the TTL of a processing idempotency record
// so that long-running business handlers do not cause the record to expire
// and allow a duplicate request to re-acquire the lock.
//
// Heartbeat is a best-effort mechanism. If a renewal fails (e.g. the record
// was already cleaned up), the heartbeat logs the error and continues —
// the record will simply expire naturally.
type Heartbeat struct {
	repo     repository.IdempotencyRecordRepository
	key      valueobject.IdempotencyKey
	scope    valueobject.Scope
	owner    valueobject.Owner
	ttl      time.Duration
	interval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// HeartbeatConfig configures the renewal cadence.
type HeartbeatConfig struct {
	Repo     repository.IdempotencyRecordRepository
	Key      valueobject.IdempotencyKey
	Scope    valueobject.Scope
	Owner    valueobject.Owner
	TTL      time.Duration // the TTL to set on each renewal (typically ProcessingTTL)
	Interval time.Duration // how often to renew; zero means TTL/2
}

// NewHeartbeat creates a Heartbeat. It does not start the renewal loop —
// call Start to begin.
func NewHeartbeat(cfg HeartbeatConfig) *Heartbeat {
	interval := cfg.Interval
	if interval <= 0 {
		interval = cfg.TTL / 2
	}
	return &Heartbeat{
		repo:     cfg.Repo,
		key:      cfg.Key,
		scope:    cfg.Scope,
		owner:    cfg.Owner,
		ttl:      cfg.TTL,
		interval: interval,
	}
}

// Start begins the renewal loop. It returns a derived context that is
// cancelled when the heartbeat stops (via Stop or panic).
func (h *Heartbeat) Start(ctx context.Context) context.Context {
	h.ctx, h.cancel = context.WithCancel(ctx)
	h.done = make(chan struct{})

	go h.loop()
	return h.ctx
}

// Stop cancels the renewal loop and blocks until the goroutine exits.
func (h *Heartbeat) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.done != nil {
		<-h.done
	}
}

func (h *Heartbeat) loop() {
	defer close(h.done)

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if err := repository.Renew(h.ctx, h.repo, h.key, h.scope, h.owner, h.ttl); err != nil {
				log.Printf("idempotency: heartbeat renew failed for key %s: %v", h.key.String(), err)
			}
		}
	}
}
