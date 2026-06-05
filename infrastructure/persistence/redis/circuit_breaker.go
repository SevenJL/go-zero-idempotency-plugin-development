package redis

import (
	"sync/atomic"
	"time"
)

// StorageFailureMode mirrors the domain service constant but avoids a
// dependency from infrastructure to domain/service.
type storageFailureMode string

const (
	storageFailClosed storageFailureMode = "fail_closed"
	storageFailOpen   storageFailureMode = "fail_open"
)

// circuitBreaker protects the application from cascading failures when Redis
// is unavailable. It tracks consecutive failures and opens the circuit when a
// threshold is exceeded.
//
// States:
//
//	closed   — normal operation, all requests go through
//	open     — consecutive failures exceeded threshold, requests are rejected
//	halfOpen — cooldown period elapsed, one trial request is allowed
//
// Concurrency: the breaker uses atomic operations for state transitions.
// In half-open state, a trialActive flag ensures only a single trial
// request is admitted — preventing the thundering-herd problem where
// multiple goroutines race through the half-open gate.
type circuitBreaker struct {
	mode         storageFailureMode
	maxFailures  int
	cooldown     time.Duration
	failureCount atomic.Int64
	lastFailure  atomic.Int64 // unix nano
	state        atomic.Int32 // 0=closed, 1=open, 2=halfOpen
	trialActive  atomic.Bool  // true when a trial is in-flight during halfOpen
}

const (
	cbStateClosed   = 0
	cbStateOpen     = 1
	cbStateHalfOpen = 2
)

// newCircuitBreaker creates a circuit breaker. mode defaults to fail_closed.
// maxFailures defaults to 5. cooldown defaults to 30s.
func newCircuitBreaker(mode storageFailureMode, maxFailures int, cooldown time.Duration) *circuitBreaker {
	if maxFailures <= 0 {
		maxFailures = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &circuitBreaker{
		mode:        mode,
		maxFailures: maxFailures,
		cooldown:    cooldown,
	}
}

// allow reports whether a request should be attempted. If the circuit is open
// and fail_open is configured, this returns false — the caller should skip the
// storage operation and return a pass-through result. If fail_closed, the
// request is always allowed (the breaker only tracks failures for observability).
func (cb *circuitBreaker) allow() bool {
	if cb.mode == storageFailClosed {
		// fail_closed: always attempt the request; failures are tracked but
		// never cause rejection. The application prefers consistency over
		// availability.
		return true
	}

	state := cb.state.Load()

	// If closed, allow everything.
	if state == cbStateClosed {
		return true
	}

	// If open, check if the cooldown has elapsed.
	if state == cbStateOpen {
		now := time.Now().UnixNano()
		last := cb.lastFailure.Load()
		if time.Duration(now-last) >= cb.cooldown {
			// Transition to half-open and allow one trial.
			cb.state.Store(cbStateHalfOpen)
			return true
		}
		return false
	}

	// Half-open: allow exactly one trial request via CAS on trialActive.
	// This prevents multiple goroutines from racing through the gate and
	// ensures only the designated trial request determines the next state.
	if !cb.trialActive.CompareAndSwap(false, true) {
		return false
	}
	return true
}

// recordSuccess resets the breaker to the closed state.
func (cb *circuitBreaker) recordSuccess() {
	cb.failureCount.Store(0)
	cb.trialActive.Store(false)
	cb.state.Store(cbStateClosed)
}

// recordFailure increments the failure count. If the threshold is reached the
// breaker transitions to the open state. The trial-active flag is cleared so
// the next cooldown cycle can admit a new trial.
func (cb *circuitBreaker) recordFailure() {
	cb.trialActive.Store(false)
	count := cb.failureCount.Add(1)
	cb.lastFailure.Store(time.Now().UnixNano())

	if count >= int64(cb.maxFailures) {
		cb.state.Store(cbStateOpen)
	}
}

// isOpen reports whether the circuit is currently open.
func (cb *circuitBreaker) isOpen() bool {
	return cb.state.Load() == cbStateOpen
}
