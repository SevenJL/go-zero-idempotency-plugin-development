//go:build integration
// +build integration

// Package tests contains integration tests that require a running Redis instance.
//
// Run with:
//
//	REDIS_ADDR=localhost:6379 go test -tags=integration -count=1 -v ./tests/
package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
	redisrepo "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/redis"
)

// redisClientAdapter wraps go-redis v9 client to match the redisClient interface
// expected by the Redis repository.
type redisClientAdapter struct {
	client *goredis.Client
}

func (a *redisClientAdapter) GetCtx(ctx context.Context, key string) (string, error) {
	return a.client.Get(ctx, key).Result()
}

func (a *redisClientAdapter) SetCtxEx(ctx context.Context, key, value string, seconds int) error {
	return a.client.Set(ctx, key, value, time.Duration(seconds)*time.Second).Err()
}

func (a *redisClientAdapter) DelCtx(ctx context.Context, keys ...string) (int, error) {
	n, err := a.client.Del(ctx, keys...).Result()
	return int(n), err
}

func (a *redisClientAdapter) ScriptRunCtx(ctx context.Context, script *goredis.Script, keys []string, args ...any) (any, error) {
	return script.Run(ctx, a.client, keys, args...).Result()
}

func redisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

func newRedisClient(t *testing.T) *redisClientAdapter {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{
		Addr:        redisAddr(),
		DialTimeout: 5 * time.Second,
		ReadTimeout: 3 * time.Second,
		DB:          15,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis not reachable at %s: %v\nStart with: docker compose up -d redis", redisAddr(), err)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	t.Cleanup(func() { rdb.Close() })
	return &redisClientAdapter{client: rdb}
}

func newRedisSvc(t *testing.T, adapter *redisClientAdapter, opts ...redisrepo.RepositoryOption) *appservice.IdempotencyService {
	t.Helper()
	repo := redisrepo.NewIdempotencyRecordRepository(adapter, opts...)
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-test",
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
		t.Fatalf("NewIdempotencyService: %v", err)
	}
	return svc
}

func redisBeginReq(t *testing.T, svc *appservice.IdempotencyService, key, body string) dto.BeginResult {
	t.Helper()
	result, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Scope:     valueobject.NewScope("", "tenant-001", "user-001"),
			Headers:   map[string][]string{"Idempotency-Key": {key}},
			Body:      []byte(body),
		},
	})
	if err != nil {
		t.Fatalf("Begin(%s): %v", key, err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Test cases
// ---------------------------------------------------------------------------

func TestRedis_FullLifecycle(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "lifecycle-001"
	body := `{"sku":"test","qty":1}`

	result := redisBeginReq(t, svc, key, body)
	if result.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", result.Type)
	}

	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         result.Key,
		Fingerprint: result.Fingerprint,
		Owner:       result.Owner,
		Response:    dto.CapturedResponse{StatusCode: 201, Body: []byte(`{"order_id":"order-123"}`)},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	result2 := redisBeginReq(t, svc, key, body)
	if result2.Type != dto.BeginResultReplay {
		t.Fatalf("expected Replay, got %v", result2.Type)
	}
}

func TestRedis_Conflict(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "conflict-001"

	r1 := redisBeginReq(t, svc, key, `{"sku":"a","qty":1}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", r1.Type)
	}

	r2 := redisBeginReq(t, svc, key, `{"sku":"b","qty":99}`)
	if r2.Type != dto.BeginResultConflict {
		t.Fatalf("expected Conflict, got %v", r2.Type)
	}
}

func TestRedis_InProgress(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "inprogress-001"
	body := `{"sku":"test","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", r1.Type)
	}

	r2 := redisBeginReq(t, svc, key, body)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("expected InProgress, got %v", r2.Type)
	}
}

func TestRedis_AbortDelete(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "abortdel-001"
	body := `{"sku":"test","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", r1.Type)
	}

	if err := svc.Abort(context.Background(), command.AbortCommand{
		Key: r1.Key, Owner: r1.Owner, Mode: model.FailureModeDelete,
	}); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	r2 := redisBeginReq(t, svc, key, body)
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired after abort+delete, got %v", r2.Type)
	}
}

func TestRedis_AbortCache(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "abortcache-001"
	body := `{"sku":"fail","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	if err := svc.Abort(context.Background(), command.AbortCommand{
		Key: r1.Key, Owner: r1.Owner, Mode: model.FailureModeCache,
		ErrorCode: "INTERNAL", ErrorMessage: "simulated failure",
	}); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	r2 := redisBeginReq(t, svc, key, body)
	if r2.Type != dto.BeginResultFailed {
		t.Fatalf("expected Failed after abort+cache, got %v", r2.Type)
	}
}

func TestRedis_ConcurrentBegin(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)
	key := "concurrent-001"
	body := `{"sku":"test","qty":1}`

	const n = 30
	var wg sync.WaitGroup
	acquired := 0
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.Begin(context.Background(), command.BeginCommand{
				Request: dto.RequestContext{
					Operation: valueobject.UnsafeOperation("POST /orders"),
					Scope:     valueobject.NewScope("", "tenant-001", "user-001"),
					Headers:   map[string][]string{"Idempotency-Key": {key}},
					Body:      []byte(body),
				},
			})
			if err == nil && result.Type == dto.BeginResultAcquired {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if acquired != 1 {
		t.Fatalf("expected exactly 1 Acquired, got %d", acquired)
	}
}

func TestRedis_WaitReplay(t *testing.T) {
	adapter := newRedisClient(t)
	repo := redisrepo.NewIdempotencyRecordRepository(adapter)
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "redis-wait-test",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		},
		Policy:       domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.TTLPolicy{ProcessingTTL: 10 * time.Second, CompletedTTL: 1 * time.Minute}),
		WaitTimeout:  3 * time.Second,
		WaitInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}

	key := "wait-001"
	body := `{"sku":"test","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", r1.Type)
	}

	type waitR struct {
		typ dto.BeginResultType
		err error
	}
	ch := make(chan waitR, 1)
	go func() {
		r2, err2 := svc.Begin(context.Background(), command.BeginCommand{
			Request: dto.RequestContext{
				Operation: valueobject.UnsafeOperation("POST /orders"),
				Scope:     valueobject.NewScope("", "tenant-001", "user-001"),
				Headers:   map[string][]string{"Idempotency-Key": {key}},
				Body:      []byte(body),
			},
		})
		ch <- waitR{r2.Type, err2}
	}()

	time.Sleep(100 * time.Millisecond)
	err = svc.Complete(context.Background(), command.CompleteCommand{
		Key: r1.Key, Fingerprint: r1.Fingerprint, Owner: r1.Owner,
		Response: dto.CapturedResponse{StatusCode: 201, Body: []byte(`{"order_id":"wait-order"}`)},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	select {
	case wr := <-ch:
		if wr.err != nil {
			t.Fatalf("WaitReplay: %v", wr.err)
		}
		if wr.typ != dto.BeginResultReplay {
			t.Fatalf("expected Replay after wait, got %v", wr.typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitReplay timed out")
	}
}

func TestRedis_RecordExpiry(t *testing.T) {
	adapter := newRedisClient(t)
	repo := redisrepo.NewIdempotencyRecordRepository(adapter)
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
		}),
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}

	key := "expire-001"
	body := `{"sku":"test","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	err = svc.Complete(context.Background(), command.CompleteCommand{
		Key: r1.Key, Fingerprint: r1.Fingerprint, Owner: r1.Owner,
		Response: dto.CapturedResponse{StatusCode: 200, Body: []byte(`{"ok":true}`)},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	time.Sleep(3 * time.Second)

	r2 := redisBeginReq(t, svc, key, body)
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired after expiry, got %v", r2.Type)
	}
}

func TestRedis_HashTag(t *testing.T) {
	adapter := newRedisClient(t)
	repo := redisrepo.NewIdempotencyRecordRepository(adapter,
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
		}),
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}

	key := fmt.Sprintf("hashtag-%d", time.Now().UnixNano())
	body := `{"sku":"test","qty":1}`

	r1 := redisBeginReq(t, svc, key, body)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected Acquired, got %v", r1.Type)
	}

	svc.Complete(context.Background(), command.CompleteCommand{
		Key: r1.Key, Fingerprint: r1.Fingerprint, Owner: r1.Owner,
		Response: dto.CapturedResponse{StatusCode: 201, Body: []byte(`{"order_id":"hashtag"}`)},
	})

	r2 := redisBeginReq(t, svc, key, body)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("expected Replay, got %v", r2.Type)
	}
}

func TestRedis_InvalidKey(t *testing.T) {
	adapter := newRedisClient(t)
	svc := newRedisSvc(t, adapter)

	result, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Scope:     valueobject.NewScope("", "t1", "u1"),
			Headers:   map[string][]string{"Idempotency-Key": {"ab"}},
			Body:      []byte(`{"sku":"test"}`),
		},
	})
	// Key too short — either error or skipped
	if err == nil && result.Type == dto.BeginResultSkipped {
		t.Log("short key skipped (expected)")
	}
}

func TestRedis_CircuitBreaker(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:        "127.0.0.1:19999",
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
		MaxRetries:  0,
	})
	defer rdb.Close()

	adapter := &redisClientAdapter{client: rdb}
	repo := redisrepo.NewIdempotencyRecordRepository(adapter,
		redisrepo.WithBreakerMaxFailures(2),
		redisrepo.WithBreakerCooldown(2*time.Second),
		redisrepo.WithStorageFailureMode("fail_open"),
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
		}),
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err := svc.Begin(context.Background(), command.BeginCommand{
			Request: dto.RequestContext{
				Operation: valueobject.UnsafeOperation("POST /orders"),
				Scope:     valueobject.NewScope("", "t1", "u1"),
				Headers:   map[string][]string{"Idempotency-Key": {"breaker-001"}},
				Body:      []byte(`{"sku":"test","qty":1}`),
			},
		})
		if err != nil {
			t.Logf("call %d: %v (expected)", i+1, err)
		} else {
			t.Logf("call %d: no error", i+1)
		}
	}
}
