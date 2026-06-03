package redis

import (
	"testing"
	"time"
)

func TestCircuitBreakerFailClosed(t *testing.T) {
	cb := newCircuitBreaker(storageFailClosed, 5, 30*time.Second)

	// fail_closed mode always allows requests.
	for i := 0; i < 100; i++ {
		if !cb.allow() {
			t.Fatalf("fail_closed: allow() returned false on iteration %d", i)
		}
		cb.recordFailure()
	}

	// After many failures, still allows (fail_closed never opens the circuit).
	if !cb.allow() {
		t.Fatal("fail_closed: allow() returned false after many failures")
	}
}

func TestCircuitBreakerFailOpenClosedState(t *testing.T) {
	cb := newCircuitBreaker(storageFailOpen, 3, 30*time.Second)

	// Initially closed — all requests allowed.
	for i := 0; i < 10; i++ {
		if !cb.allow() {
			t.Fatalf("fail_open closed state: allow() returned false on iteration %d", i)
		}
	}
}

func TestCircuitBreakerFailOpenOpensAfterThreshold(t *testing.T) {
	cb := newCircuitBreaker(storageFailOpen, 3, 30*time.Second)

	// Record failures up to threshold.
	cb.recordFailure()
	cb.recordFailure()

	// Still closed — threshold not reached.
	if !cb.allow() {
		t.Fatal("fail_open: closed prematurely (2/3 failures)")
	}

	// 3rd failure opens the circuit.
	cb.recordFailure()
	if !cb.isOpen() {
		t.Fatal("fail_open: circuit did not open after 3 failures")
	}

	// Now requests should be rejected.
	if cb.allow() {
		t.Fatal("fail_open: allow() returned true while open")
	}
}

func TestCircuitBreakerHalfOpenTransitions(t *testing.T) {
	cb := newCircuitBreaker(storageFailOpen, 2, 50*time.Millisecond)

	// Trip the breaker.
	cb.recordFailure()
	cb.recordFailure()
	if !cb.isOpen() {
		t.Fatal("circuit did not open")
	}

	// Wait for cooldown.
	time.Sleep(60 * time.Millisecond)

	// First request after cooldown: half-open, one trial allowed.
	if !cb.allow() {
		t.Fatal("half-open: first trial request rejected")
	}

	// On success, transition back to closed.
	cb.recordSuccess()
	if cb.isOpen() {
		t.Fatal("circuit should be closed after success in half-open")
	}

	// Subsequent requests allowed.
	if !cb.allow() {
		t.Fatal("closed after half-open: request rejected")
	}
}

func TestCircuitBreakerHalfOpenFailureReturnsToOpen(t *testing.T) {
	cb := newCircuitBreaker(storageFailOpen, 2, 50*time.Millisecond)

	// Trip the breaker.
	cb.recordFailure()
	cb.recordFailure()

	// Wait for cooldown.
	time.Sleep(60 * time.Millisecond)

	// Half-open trial request.
	if !cb.allow() {
		t.Fatal("half-open: first trial rejected")
	}

	// Trial fails — back to open.
	cb.recordFailure()
	if !cb.isOpen() {
		t.Fatal("circuit should be open after half-open failure")
	}
}

func TestCircuitBreakerDefaults(t *testing.T) {
	// Zero/negative values use defaults.
	cb := newCircuitBreaker(storageFailClosed, 0, 0)

	// Should have default maxFailures of 5 and cooldown of 30s.
	if cb.maxFailures != 5 {
		t.Errorf("default maxFailures = %d, want 5", cb.maxFailures)
	}
	if cb.cooldown != 30*time.Second {
		t.Errorf("default cooldown = %v, want 30s", cb.cooldown)
	}
}
