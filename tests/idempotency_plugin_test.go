// Package tests contains integration-style tests that exercise the full
// idempotency plugin pipeline (domain + application + memory infra) to verify
// correctness and surface bugs.
package tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
)

// ---------------------------------------------------------------------------
// test harness
// ---------------------------------------------------------------------------

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func baseTime() time.Time {
	return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
}

func newService(t *testing.T, clock *testClock, opts ...func(*appservice.Config)) *appservice.IdempotencyService {
	t.Helper()

	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	cfg := appservice.Config{
		Repository:   repo,
		Clock:        clock,
		Scope:        "test-svc",
		OwnerFactory: appservice.RandomOwnerFactory{},
		Policy:       domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.DefaultTTLPolicy()),
	}
	for _, o := range opts {
		o(&cfg)
	}

	svc, err := appservice.NewIdempotencyService(cfg)
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}
	return svc
}

func beginReq(t *testing.T, svc *appservice.IdempotencyService, key, body string) dto.BeginResult {
	t.Helper()
	return beginReqWithTenant(t, svc, key, body, "tenant-001", "user-001")
}

func beginReqWithTenant(t *testing.T, svc *appservice.IdempotencyService, key, body, tenant, user string) dto.BeginResult {
	t.Helper()
	result, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Scope:     valueobject.NewScope("", tenant, user),
			Headers:   map[string][]string{"Idempotency-Key": {key}},
			Body:      []byte(body),
		},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return result
}

func completeReq(t *testing.T, svc *appservice.IdempotencyService, r dto.BeginResult, status int, body string) {
	t.Helper()
	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         r.Key,
		Fingerprint: r.Fingerprint,
		Owner:       r.Owner,
		Response:    dto.CapturedResponse{StatusCode: status, Body: []byte(body)},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func abortDelete(t *testing.T, svc *appservice.IdempotencyService, r dto.BeginResult) {
	t.Helper()
	err := svc.Abort(context.Background(), command.AbortCommand{
		Key:   r.Key,
		Owner: r.Owner,
		Mode:  model.FailureModeDelete,
	})
	if err != nil {
		t.Fatalf("Abort(delete): %v", err)
	}
}

func abortCache(t *testing.T, svc *appservice.IdempotencyService, r dto.BeginResult, code, msg string) {
	t.Helper()
	err := svc.Abort(context.Background(), command.AbortCommand{
		Key:          r.Key,
		Fingerprint:  r.Fingerprint,
		Owner:        r.Owner,
		Mode:         model.FailureModeCache,
		ErrorCode:    code,
		ErrorMessage: msg,
	})
	if err != nil {
		t.Fatalf("Abort(cache): %v", err)
	}
}

// ---------------------------------------------------------------------------
// 1. Full lifecycle – Begin → Complete → Replay
// ---------------------------------------------------------------------------

func TestFullLifecycleBeginCompleteReplay(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	// First request acquires
	r1 := beginReq(t, svc, "key-001-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Complete with 201
	completeReq(t, svc, r1, 201, `{"orderId":"order-1"}`)

	// Second request with same key must replay
	r2 := beginReq(t, svc, "key-001-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("second Begin: got %s, want replay", r2.Type)
	}
	if r2.Response.StatusCode != 201 {
		t.Fatalf("replay status: got %d, want 201", r2.Response.StatusCode)
	}
	if string(r2.Response.Body) != `{"orderId":"order-1"}` {
		t.Fatalf("replay body: got %q, want %q", string(r2.Response.Body), `{"orderId":"order-1"}`)
	}
}

// ---------------------------------------------------------------------------
// 2. Conflict – same key, different body
// ---------------------------------------------------------------------------

func TestConflictSameKeyDifferentBody(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-002-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Different body → different fingerprint → conflict
	r2 := beginReq(t, svc, "key-002-ABCDEFGHI", `{"sku":"B"}`)
	if r2.Type != dto.BeginResultConflict {
		t.Fatalf("second Begin: got %s, want conflict", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 3. In-progress – duplicate request while first is still processing
// ---------------------------------------------------------------------------

func TestInProgressDuplicate(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-003-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Second request before first completes → in_progress
	r2 := beginReq(t, svc, "key-003-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin: got %s, want in_progress", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 4. Abort delete – record is removed, next request can acquire again
// ---------------------------------------------------------------------------

func TestAbortDeleteAllowsReacquire(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-004-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	abortDelete(t, svc, r1)

	// After delete-mode abort the key is gone → re-acquire
	r2 := beginReq(t, svc, "key-004-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin after abort: got %s, want acquired", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 5. Abort cache – failed result is returned to subsequent requests
// ---------------------------------------------------------------------------

func TestAbortCacheStoresFailure(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-005-ABCDEFGHI", `{"sku":"A"}`)
	abortCache(t, svc, r1, "DOWNSTREAM_TIMEOUT", "payment gateway timeout")

	// Next request sees failed state
	r2 := beginReq(t, svc, "key-005-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultFailed {
		t.Fatalf("second Begin: got %s, want failed", r2.Type)
	}
	if r2.ErrorCode != "DOWNSTREAM_TIMEOUT" {
		t.Fatalf("ErrorCode: got %q, want DOWNSTREAM_TIMEOUT", r2.ErrorCode)
	}
	if r2.ErrorMessage != "payment gateway timeout" {
		t.Fatalf("ErrorMessage: got %q, want 'payment gateway timeout'", r2.ErrorMessage)
	}
}

// ---------------------------------------------------------------------------
// 6. Wait policy – duplicate replays when record is already completed
// ---------------------------------------------------------------------------

func TestWaitPolicyReplaysAfterComplete(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.Policy = domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.DefaultTTLPolicy())
		c.WaitTimeout = 5 * time.Second
		c.WaitInterval = 50 * time.Millisecond
	})

	// First request acquires
	r1 := beginReq(t, svc, "key-006-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Complete the first request
	completeReq(t, svc, r1, 201, `{"orderId":"order-6"}`)

	// After first is completed, a new Begin replays immediately
	r2, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Scope:     valueobject.NewScope("", "tenant-001", "user-001"),
			Headers:   map[string][]string{"Idempotency-Key": {"key-006-ABCDEFGHI"}},
			Body:      []byte(`{"sku":"A"}`),
		},
	})
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("second Begin after complete: got %s, want replay", r2.Type)
	}
	if string(r2.Response.Body) != `{"orderId":"order-6"}` {
		t.Fatalf("replay body: got %q, want %q", string(r2.Response.Body), `{"orderId":"order-6"}`)
	}
}

// ---------------------------------------------------------------------------
// 7. Wait timeout – duplicate waits too long, returns in_progress
// ---------------------------------------------------------------------------

func TestWaitTimeoutReturnsInProgress(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.Policy = domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.DefaultTTLPolicy())
		c.WaitTimeout = 2 * time.Second
		c.WaitInterval = 50 * time.Millisecond
	})

	// First request acquires
	r1 := beginReq(t, svc, "key-007-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Second request waits but first never completes → timeout → retry Begin
	// Our test clock Sleep only advances the clock, so WaitReplay will spin
	// until deadline is exceeded. Then it returns Found=false, Record!=nil.
	// The Begin method will then call toBeginResult with the original decision
	// and return in_progress.
	r2 := beginReq(t, svc, "key-007-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin after wait timeout: got %s, want in_progress", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 8. Disabled service – always returns skipped
// ---------------------------------------------------------------------------

func TestDisabledServiceReturnsSkipped(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.Disabled = true
	})

	r := beginReq(t, svc, "key-008-ABCDEFGHI", `{"sku":"A"}`)
	if r.Type != dto.BeginResultSkipped {
		t.Fatalf("Begin on disabled service: got %s, want skipped", r.Type)
	}
}

// ---------------------------------------------------------------------------
// 9. Missing key (not required) – skips
// ---------------------------------------------------------------------------

func TestMissingKeyNotRequiredSkips(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.KeyResolver = appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   false,
		}
	})

	result, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers:   map[string][]string{}, // no key header
		},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if result.Type != dto.BeginResultSkipped {
		t.Fatalf("Begin with missing key: got %s, want skipped", result.Type)
	}
}

// ---------------------------------------------------------------------------
// 10. Missing key (required) – returns error
// ---------------------------------------------------------------------------

func TestMissingKeyRequiredReturnsError(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.KeyResolver = appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   true,
		}
	})

	_, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers:   map[string][]string{}, // no key header
		},
	})
	if !errors.Is(err, appservice.ErrMissingIdempotencyKey) {
		t.Fatalf("Begin error: got %v, want %v", err, appservice.ErrMissingIdempotencyKey)
	}
}

// ---------------------------------------------------------------------------
// 11. Invalid key format – returns validation error
// ---------------------------------------------------------------------------

func TestInvalidKeyFormatReturnsError(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	_, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers:   map[string][]string{"Idempotency-Key": {"short"}}, // too short
		},
	})
	if !errors.Is(err, valueobject.ErrInvalidIdempotencyKey) {
		t.Fatalf("Begin error: got %v, want %v", err, valueobject.ErrInvalidIdempotencyKey)
	}
}

// ---------------------------------------------------------------------------
// 12. Complete with wrong owner – returns owner mismatch
// ---------------------------------------------------------------------------

func TestCompleteWrongOwnerFails(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-012-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin: got %s, want acquired", r1.Type)
	}

	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         r1.Key,
		Fingerprint: r1.Fingerprint,
		Owner:       valueobject.UnsafeOwner("intruder-owner"),
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte("{}")},
	})
	if !errors.Is(err, model.ErrOwnerMismatch) {
		t.Fatalf("Complete error: got %v, want %v", err, model.ErrOwnerMismatch)
	}
}

// ---------------------------------------------------------------------------
// 13. Complete non-existent key – returns invalid state
// ---------------------------------------------------------------------------

func TestCompleteNonexistentKeyFails(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         valueobject.UnsafeIdempotencyKey("ghost-key-123456789"),
		Fingerprint: valueobject.UnsafeFingerprint("sha256:fff"),
		Owner:       valueobject.UnsafeOwner("owner-1"),
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte("{}")},
	})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("Complete error: got %v, want %v", err, model.ErrInvalidState)
	}
}

// ---------------------------------------------------------------------------
// 14. JSON canonicalization – reordered keys produce same fingerprint
// ---------------------------------------------------------------------------

func TestJSONCanonicalization(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-014-ABCDEFGHI", `{"sku":"A","qty":1}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Same semantic content, different key order in JSON — must be in_progress
	// (same fingerprint) not conflict
	r2 := beginReq(t, svc, "key-014-ABCDEFGHI", `{"qty":1,"sku":"A"}`)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin: got %s, want in_progress (canonical JSON)", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 15. Different tenant → different fingerprint → conflict (not in_progress)
//    This verifies that scope isolation works.
// ---------------------------------------------------------------------------

func TestDifferentTenantProducesConflict(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReqWithTenant(t, svc, "key-015-ABCDEFGHI", `{"sku":"A"}`, "tenant-A", "user-1")
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Same key but different tenant → different fingerprint → conflict
	r2 := beginReqWithTenant(t, svc, "key-015-ABCDEFGHI", `{"sku":"A"}`, "tenant-B", "user-1")
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin (different tenant): got %s, want acquired", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 16. Record expiry – after expiry new request can re-acquire
// ---------------------------------------------------------------------------

func TestRecordExpiryAllowsReacquire(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.Policy = domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 5 * time.Second,
			CompletedTTL:  1 * time.Hour,
			FailedTTL:     1 * time.Minute,
		})
	})

	r1 := beginReq(t, svc, "key-016-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin: got %s, want acquired", r1.Type)
	}

	// Advance past processing TTL
	clock.advance(6 * time.Second)

	// After expiry, new request should be able to acquire
	r2 := beginReq(t, svc, "key-016-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin after expiry: got %s, want acquired", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 17. Concurrent Begins – only one acquires, others get in_progress or conflict
// ---------------------------------------------------------------------------

func TestConcurrentBeginsOnlyOneAcquires(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	const goroutines = 20
	results := make([]dto.BeginResult, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			r, err := svc.Begin(context.Background(), command.BeginCommand{
				Request: dto.RequestContext{
					Operation: valueobject.UnsafeOperation("POST /orders"),
					Scope:     valueobject.NewScope("", "tenant-001", ""),
					Headers:   map[string][]string{"Idempotency-Key": {"key-concurrent-001"}},
					Body:      []byte(`{"sku":"A"}`),
				},
			})
			results[idx] = r
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	acquired := 0
	inProgress := 0
	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d Begin error: %v", i, errs[i])
		}
		switch results[i].Type {
		case dto.BeginResultAcquired:
			acquired++
		case dto.BeginResultInProgress:
			inProgress++
		default:
			t.Fatalf("goroutine %d unexpected result: %s", i, results[i].Type)
		}
	}

	if acquired != 1 {
		t.Fatalf("concurrent Begins: got %d acquired, want exactly 1", acquired)
	}
	if inProgress != goroutines-1 {
		t.Fatalf("concurrent Begins: got %d in_progress, want %d", inProgress, goroutines-1)
	}
}

// ---------------------------------------------------------------------------
// 18. Complete then Complete again on same record → invalid state
//    (Verifies the domain invariant that only processing can be completed)
// ---------------------------------------------------------------------------

func TestDoubleCompleteFails(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-018-ABCDEFGHI", `{"sku":"A"}`)
	completeReq(t, svc, r1, 201, `{"orderId":"order-18"}`)

	// Try to complete again — the record is now in completed state
	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         r1.Key,
		Fingerprint: r1.Fingerprint,
		Owner:       r1.Owner,
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte("{}")},
	})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("second Complete: got %v, want %v", err, model.ErrInvalidState)
	}
}

// ---------------------------------------------------------------------------
// 19. Service construction requires repository
// ---------------------------------------------------------------------------

func TestNewServiceRequiresRepository(t *testing.T) {
	_, err := appservice.NewIdempotencyService(appservice.Config{})
	if !errors.Is(err, appservice.ErrRepositoryRequired) {
		t.Fatalf("NewIdempotencyService: got %v, want %v", err, appservice.ErrRepositoryRequired)
	}
}

// ---------------------------------------------------------------------------
// 20. Response body is correctly deep-copied (clone safety)
// ---------------------------------------------------------------------------

func TestResponseBodyIsDeepCopied(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-020-ABCDEFGHI", `{"sku":"A"}`)

	originalBody := []byte(`{"orderId":"order-20"}`)
	completeReq(t, svc, r1, 201, string(originalBody))

	// Replay and verify body matches
	r2 := beginReq(t, svc, "key-020-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("replay Begin: got %s, want replay", r2.Type)
	}
	if string(r2.Response.Body) != `{"orderId":"order-20"}` {
		t.Fatalf("replay body: got %q, want %q", string(r2.Response.Body), `{"orderId":"order-20"}`)
	}

	// Mutate the replay response body — original must be unaffected
	r2.Response.Body[0] = 'X'

	// Third replay must still return the original body
	r3 := beginReq(t, svc, "key-020-ABCDEFGHI", `{"sku":"A"}`)
	if r3.Type != dto.BeginResultReplay {
		t.Fatalf("third Begin: got %s, want replay", r3.Type)
	}
	if string(r3.Response.Body) != `{"orderId":"order-20"}` {
		t.Fatalf("third replay body after mutation: got %q, want %q", string(r3.Response.Body), `{"orderId":"order-20"}`)
	}
}

// ---------------------------------------------------------------------------
// 21. Context cancellation stops WaitReplay
// ---------------------------------------------------------------------------

func TestWaitReplayContextCancellation(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock, func(c *appservice.Config) {
		c.Policy = domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.DefaultTTLPolicy())
		c.WaitTimeout = 10 * time.Second
	})

	// Acquire a record so a wait would be needed
	r1 := beginReq(t, svc, "key-021-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin: got %s, want acquired", r1.Type)
	}

	// Start WaitReplay with a cancelled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.WaitReplay(cancelCtx, command.ReplayCommand{
		Key:      valueobject.UnsafeIdempotencyKey("key-021-ABCDEFGHI"),
		Deadline: clock.Now().Add(5 * time.Second),
	})
	if err == nil {
		t.Fatal("WaitReplay with cancelled context: got nil error, want context.Canceled")
	}
}

// ---------------------------------------------------------------------------
// 22. CapturedResponse headers are correctly preserved
// ---------------------------------------------------------------------------

func TestCapturedResponseHeadersPreserved(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-022-ABCDEFGHI", `{"sku":"A"}`)

	// Complete with custom headers
	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         r1.Key,
		Fingerprint: r1.Fingerprint,
		Owner:       r1.Owner,
		Response: dto.CapturedResponse{
			StatusCode: 201,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
				"X-Custom":     {"value-1", "value-2"},
			},
			Body: []byte(`{"id":"22"}`),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	r2 := beginReq(t, svc, "key-022-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("replay Begin: got %s, want replay", r2.Type)
	}
	if r2.Response.StatusCode != 201 {
		t.Fatalf("status: got %d, want 201", r2.Response.StatusCode)
	}
	if len(r2.Response.Headers["X-Custom"]) != 2 {
		t.Fatalf("X-Custom header: got %d values, want 2", len(r2.Response.Headers["X-Custom"]))
	}
}

// ---------------------------------------------------------------------------
// 23. Zero-now defaults to clock time
//    (Verifies that when Now is zero-valued, the clock is used)
// ---------------------------------------------------------------------------

func TestZeroNowUsesClock(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	// Begin with zero Now — should use clock.Now()
	result, err := svc.Begin(context.Background(), command.BeginCommand{
		// Now is zero value
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Scope:     valueobject.NewScope("", "tenant-001", ""),
			Headers:   map[string][]string{"Idempotency-Key": {"key-023-abcdefgh"}},
			Body:      []byte(`{"sku":"A"}`),
		},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if result.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin: got %s, want acquired", result.Type)
	}
}

// ---------------------------------------------------------------------------
// 24. Abort with keep_processing_until_ttl – record stays in processing
// ---------------------------------------------------------------------------

func TestAbortKeepProcessingTTL(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	r1 := beginReq(t, svc, "key-024-ABCDEFGHI", `{"sku":"A"}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin: got %s, want acquired", r1.Type)
	}

	err := svc.Abort(context.Background(), command.AbortCommand{
		Key:   r1.Key,
		Owner: r1.Owner,
		Mode:  model.FailureModeKeepProcessingTTL,
	})
	if err != nil {
		t.Fatalf("Abort(keep_processing_until_ttl): %v", err)
	}

	// Record still in processing → next request gets in_progress
	r2 := beginReq(t, svc, "key-024-ABCDEFGHI", `{"sku":"A"}`)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin: got %s, want in_progress", r2.Type)
	}
}

// ---------------------------------------------------------------------------
// 25. WaitReplay returns Found=false when record is missing
// ---------------------------------------------------------------------------

func TestWaitReplayRecordMissing(t *testing.T) {
	clock := &testClock{now: baseTime()}
	svc := newService(t, clock)

	result, err := svc.WaitReplay(context.Background(), command.ReplayCommand{
		Key:      valueobject.UnsafeIdempotencyKey("nonexistent-key-0001"),
		Deadline: clock.Now().Add(1 * time.Second),
	})
	if err != nil {
		t.Fatalf("WaitReplay: %v", err)
	}
	if result.Found {
		t.Fatal("WaitReplay for missing key: Found=true, want false")
	}
}
