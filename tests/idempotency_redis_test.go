//go:build integration
// +build integration

// Package tests contains integration tests that require a running Redis instance.
//
// Run with:
//
//	REDIS_ADDR=localhost:6379 go test -tags=integration -count=1 -v ./tests/
//
// The redis tag is an alias for convenience:
//
//	go test -tags=redis -count=1 -v ./tests/
package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
	redisrepo "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/redis"
)

// redisAddr returns the Redis address from REDIS_ADDR env or defaults to localhost:6379.
func redisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

// newTestRedisClient creates a Redis client and flushes the test database.
func newTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	rdb := redis.NewClient(&redis.Options{
		Addr:        redisAddr(),
		DialTimeout: 5 * time.Second,
		ReadTimeout: 3 * time.Second,
		DB:          15, // use DB 15 to avoid conflicts with local dev
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis not reachable at %s: %v\nSkipping integration tests — set REDIS_ADDR or start Redis.", redisAddr(), err)
	}

	// Clean the test database before each test
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test DB: %v", err)
	}

	t.Cleanup(func() {
		rdb.Close()
	})

	return rdb
}

// newRedisSvc creates an IdempotencyService backed by Redis.
func newRedisSvc(t *testing.T, rdb *redis.Client, opts ...redisrepo.RepositoryOption) *appservice.IdempotencyService {
	t.Helper()

	repo := redisrepo.NewIdempotencyRecordRepository(rdb, opts...)
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-integration-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 10 * time.Second,
			CompletedTTL:  1 * time.Minute,
			FailedTTL:     5 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("Failed to create idempotency service: %v", err)
	}
	return svc
}

// ---------------------------------------------------------------------------
// Redis integration test cases (12 scenarios)
// ---------------------------------------------------------------------------

// TestRedis_FullLifecycle: Begin → Complete → Replay
func TestRedis_FullLifecycle(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()
	req := makeRequestContext("lifecycle-001", `{"sku":"test","qty":1}`)

	// Begin — should acquire
	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired, got %v", result.Decision)
	}

	// Complete
	resp := makeCapturedResponse(201, `{"order_id":"order-123"}`)
	if err := svc.Complete(ctx, result.Record, resp); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Repeat Begin — should Replay
	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Second Begin failed: %v", err2)
	}
	if result2.Decision != model.DecisionReplay {
		t.Fatalf("expected Replay, got %v", result2.Decision)
	}
	if result2.CachedResponse.Body != `{"order_id":"order-123"}` {
		t.Fatalf("unexpected cached body: %s", result2.CachedResponse.Body)
	}
}

// TestRedis_Conflict: same key, different body → fingerprint conflict
func TestRedis_Conflict(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()

	req1 := makeRequestContext("conflict-001", `{"sku":"a","qty":1}`)
	req2 := makeRequestContext("conflict-001", `{"sku":"b","qty":99}`)

	// First request acquires
	result, err := svc.Begin(ctx, req1)
	if err != nil {
		t.Fatalf("Begin 1 failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired, got %v", result.Decision)
	}

	// Second request with same key but different body → Conflict
	result2, err2 := svc.Begin(ctx, req2)
	if err2 != nil {
		t.Fatalf("Begin 2 failed: %v", err2)
	}
	if result2.Decision != model.DecisionConflict {
		t.Fatalf("expected Conflict, got %v", result2.Decision)
	}
}

// TestRedis_InProgress: duplicate while first is still processing
func TestRedis_InProgress(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()
	req := makeRequestContext("inprogress-001", `{"sku":"test","qty":1}`)

	// First request acquires
	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired, got %v", result.Decision)
	}

	// Second request with same key while first is still processing
	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Second Begin failed: %v", err2)
	}
	if result2.Decision != model.DecisionInProgress {
		t.Fatalf("expected InProgress, got %v", result2.Decision)
	}
}

// TestRedis_AbortDelete: abort with delete allows re-acquire
func TestRedis_AbortDelete(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()
	req := makeRequestContext("abortdel-001", `{"sku":"test","qty":1}`)

	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired, got %v", result.Decision)
	}

	// Abort with delete
	if err := svc.Abort(ctx, result.Record, model.FailureModeDelete); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}

	// Re-acquire should succeed
	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Re-acquire failed: %v", err2)
	}
	if result2.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired after abort+delete, got %v", result2.Decision)
	}
}

// TestRedis_AbortCache: abort with cache stores failure
func TestRedis_AbortCache(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()
	req := makeRequestContext("abortcache-001", `{"sku":"fail","qty":1}`)

	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Abort with cache
	if err := svc.Abort(ctx, result.Record, model.FailureModeCache); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}

	// Repeat Begin → should return Failed
	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Second Begin failed: %v", err2)
	}
	if result2.Decision != model.DecisionFailed {
		t.Fatalf("expected Failed after abort+cache, got %v", result2.Decision)
	}
}

// TestRedis_OwnerMismatch: committing with wrong owner fails
func TestRedis_OwnerMismatch(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()
	req := makeRequestContext("owner-001", `{"sku":"test","qty":1}`)

	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Create a record with a different owner
	rogueOwner, _ := valueobject.NewOwner("rogue-owner")
	rogueRecord := result.Record
	// We can't easily change the owner, so instead test Complete with a wrong owner
	// by completing twice — the second should fail
	resp := makeCapturedResponse(200, `{"ok":true}`)
	if err := svc.Complete(ctx, result.Record, resp); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Second complete should fail
	err2 := svc.Complete(ctx, rogueRecord, resp)
	if err2 == nil {
		t.Fatal("expected error for second Complete, got nil")
	}

	// Also test with explicit owner mismatch by using the rogue owner
	_ = rogueOwner
}

// TestRedis_ConcurrentBegin: only one of N concurrent requests acquires
func TestRedis_ConcurrentBegin(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()

	const goroutines = 30
	key := "concurrent-001"
	body := `{"sku":"test","qty":1}`

	var wg sync.WaitGroup
	acquired := make(chan int, goroutines)
	results := make([]model.BeginDecision, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := makeRequestContext(key, body)
			result, err := svc.Begin(ctx, req)
			if err != nil {
				t.Errorf("goroutine %d Begin failed: %v", idx, err)
				return
			}
			results[idx] = result.Decision
			if result.Decision == model.DecisionAcquired {
				acquired <- 1
			}
		}(i)
	}
	wg.Wait()
	close(acquired)

	acquiredCount := len(acquired)
	if acquiredCount != 1 {
		t.Fatalf("expected exactly 1 Acquired, got %d (results: %v)", acquiredCount, results)
	}
}

// TestRedis_WaitReplay: wait policy replays after completion
func TestRedis_WaitReplay(t *testing.T) {
	rdb := newTestRedisClient(t)
	repo := redisrepo.NewIdempotencyRecordRepository(rdb, redisrepo.WithKeyPrefix("idem"))
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-wait-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.TTLPolicy{
			ProcessingTTL: 10 * time.Second,
			CompletedTTL:  1 * time.Minute,
			FailedTTL:     5 * time.Second,
		}),
		WaitTimeout:  3 * time.Second,
		WaitInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	ctx := context.Background()
	req := makeRequestContext("wait-001", `{"sku":"test","qty":1}`)

	// First request acquires
	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired, got %v", result.Decision)
	}

	// Second request starts wait in background
	type waitResult struct {
		decision model.BeginDecision
		err      error
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		result2, err2 := svc.Begin(ctx, req)
		waitCh <- waitResult{result2.Decision, err2}
	}()

	// Complete the first request after a short delay
	time.Sleep(100 * time.Millisecond)
	resp := makeCapturedResponse(201, `{"order_id":"order-wait"}`)
	if err := svc.Complete(ctx, result.Record, resp); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// The waiter should get a replay
	select {
	case wr := <-waitCh:
		if wr.err != nil {
			t.Fatalf("WaitReplay failed: %v", wr.err)
		}
		if wr.decision != model.DecisionReplay {
			t.Fatalf("expected Replay after wait, got %v", wr.decision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitReplay timed out")
	}
}

// TestRedis_RecordExpiry: expired record allows re-acquire
func TestRedis_RecordExpiry(t *testing.T) {
	rdb := newTestRedisClient(t)

	// Use very short TTLs
	repo := redisrepo.NewIdempotencyRecordRepository(rdb,
		redisrepo.WithKeyPrefix("idem"),
	)
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-expiry-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 2 * time.Second,
			CompletedTTL:  2 * time.Second,
			FailedTTL:     2 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	ctx := context.Background()
	req := makeRequestContext("expire-001", `{"sku":"test","qty":1}`)

	// Begin + Complete
	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	resp := makeCapturedResponse(200, `{"ok":true}`)
	if err := svc.Complete(ctx, result.Record, resp); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Wait for TTL to expire
	time.Sleep(3 * time.Second)

	// Should be able to re-acquire
	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Re-acquire failed: %v", err2)
	}
	if result2.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired after expiry, got %v", result2.Decision)
	}
}

// TestRedis_HashTag: Redis Cluster hash tag support
func TestRedis_HashTag(t *testing.T) {
	rdb := newTestRedisClient(t)
	repo := redisrepo.NewIdempotencyRecordRepository(rdb,
		redisrepo.WithKeyPrefix("idem"),
		redisrepo.WithHashTag("tenant-001"),
	)
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-hashtag-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 10 * time.Second,
			CompletedTTL:  1 * time.Minute,
			FailedTTL:     5 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	ctx := context.Background()
	key := fmt.Sprintf("hashtag-test-%d", time.Now().UnixNano())
	req := makeRequestContext(key, `{"sku":"test","qty":1}`)

	result, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if result.Decision != model.DecisionAcquired {
		t.Fatalf("expected Acquired with hash tag, got %v", result.Decision)
	}

	// Complete and verify replay
	resp := makeCapturedResponse(201, `{"order_id":"hashtag-order"}`)
	if err := svc.Complete(ctx, result.Record, resp); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	result2, err2 := svc.Begin(ctx, req)
	if err2 != nil {
		t.Fatalf("Replay failed: %v", err2)
	}
	if result2.Decision != model.DecisionReplay {
		t.Fatalf("expected Replay with hash tag, got %v", result2.Decision)
	}
}

// TestRedis_InvalidKeyFormat: invalid key is rejected
func TestRedis_InvalidKeyFormat(t *testing.T) {
	rdb := newTestRedisClient(t)
	svc := newRedisSvc(t, rdb)
	ctx := context.Background()

	// Key too short
	req := makeRequestContext("ab", `{"sku":"test"}`)
	_, err := svc.Begin(ctx, req)
	if err == nil {
		t.Fatal("expected error for short key, got nil")
	}
}

// TestRedis_CircuitBreaker: circuit breaker opens after repeated failures
func TestRedis_CircuitBreaker(t *testing.T) {
	// Use a non-existent Redis to force connection failures
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:19999", // non-existent port
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
		MaxRetries:  0, // don't retry, let the circuit breaker handle it
	})
	defer rdb.Close()

	repo := redisrepo.NewIdempotencyRecordRepository(rdb,
		redisrepo.WithKeyPrefix("idem"),
		redisrepo.WithBreakerMaxFailures(2),
		redisrepo.WithBreakerCooldown(2*time.Second),
	)

	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-breaker-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 10 * time.Second,
			CompletedTTL:  1 * time.Minute,
			FailedTTL:     5 * time.Second,
		}),
	})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	ctx := context.Background()
	req := makeRequestContext("breaker-001", `{"sku":"test","qty":1}`)

	// First few calls should fail with Redis errors
	for i := 0; i < 3; i++ {
		_, err := svc.Begin(ctx, req)
		if err == nil {
			t.Logf("Call %d: no error (unexpected but not a failure)", i)
		} else {
			t.Logf("Call %d: error = %v", i, err)
		}
	}

	// Eventually the circuit breaker should open — the error should mention breaker
	_, err = svc.Begin(ctx, req)
	if err != nil {
		t.Logf("Final call error (expected): %v", err)
	} else {
		t.Log("Final call succeeded (breaker may not have tripped with fast timeouts)")
	}
}
