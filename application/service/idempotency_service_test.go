package service_test

import (
	"context"
	"errors"
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

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	return c.now
}

func (c *fixedClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
}

type fixedOwnerFactory struct {
	owner valueobject.Owner
}

func (f fixedOwnerFactory) NewOwner(context.Context) (valueobject.Owner, error) {
	return f.owner, nil
}

// ---- Lifecycle edge cases ----

func TestIdempotencyServiceDisabled(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newDisabledService(t, repo, clock)

	result := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if result.Type != dto.BeginResultSkipped {
		t.Fatalf("disabled Begin() type = %s, want %s", result.Type, dto.BeginResultSkipped)
	}
}

func TestIdempotencyServiceBeginMissingKeyError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	_, err := beginOrderNoKey(t, svc, ctx, []byte(`{"sku":"A"}`))
	if err == nil {
		t.Fatal("expected error for missing idempotency key")
	}
	if !errors.Is(err, appservice.ErrMissingIdempotencyKey) {
		t.Fatalf("error = %v, want ErrMissingIdempotencyKey", err)
	}
}

func TestIdempotencyServiceBeginCompleteReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       begin.Owner,
		Response:    dto.CapturedResponse{StatusCode: 201, Body: []byte(`{"id":"order-1"}`)},
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	replay := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if replay.Type != dto.BeginResultReplay {
		t.Fatalf("second Begin() type = %s, want %s", replay.Type, dto.BeginResultReplay)
	}
	if replay.Response.StatusCode != 201 {
		t.Fatalf("replay status = %d, want 201", replay.Response.StatusCode)
	}
	if string(replay.Response.Body) != `{"id":"order-1"}` {
		t.Fatalf("replay body = %q", string(replay.Response.Body))
	}
}

func TestIdempotencyServiceBeginConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	second := beginOrder(t, svc, ctx, []byte(`{"sku":"B"}`))
	if second.Type != dto.BeginResultConflict {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultConflict)
	}
}

func TestIdempotencyServiceCanonicalizesJSONFingerprint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A","qty":1}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	second := beginOrder(t, svc, ctx, []byte(`{"qty":1,"sku":"A"}`))
	if second.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultInProgress)
	}
}

func TestIdempotencyServiceBeginInProgress(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultInProgress)
	}
}

func TestIdempotencyServiceWaitReturnsFailedResult(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newWaitService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Abort(ctx, command.AbortCommand{
		Key:          begin.Key,
		Fingerprint:  begin.Fingerprint,
		Owner:        begin.Owner,
		Mode:         model.FailureModeCache,
		ErrorCode:    "INTERNAL",
		ErrorMessage: "create order failed",
		Now:          now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Abort() error = %v", err)
	}

	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultFailed {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultFailed)
	}
	if second.ErrorCode != "INTERNAL" {
		t.Fatalf("second ErrorCode = %q, want INTERNAL", second.ErrorCode)
	}
}

// ---- Abort variants ----

func TestIdempotencyServiceAbortDelete(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Abort(ctx, command.AbortCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       begin.Owner,
		Mode:        model.FailureModeDelete,
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Abort(delete) error = %v", err)
	}

	// After delete, a new begin with the same key should acquire
	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin() after delete type = %s, want %s", second.Type, dto.BeginResultAcquired)
	}
}

func TestIdempotencyServiceAbortDefaultMode(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	// Empty mode should default to FailureModeDelete
	err := svc.Abort(ctx, command.AbortCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       begin.Owner,
		Mode:        "",
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Abort(default) error = %v", err)
	}

	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin() after default abort type = %s, want %s", second.Type, dto.BeginResultAcquired)
	}
}

func TestIdempotencyServiceAbortKeepProcessingTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Abort(ctx, command.AbortCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       begin.Owner,
		Mode:        model.FailureModeKeepProcessingTTL,
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Abort(keep_processing_ttl) error = %v", err)
	}
}

func TestIdempotencyServiceAbortRecordNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	// Abort with FailureModeCache goes through Find → MarkFailed → Commit path
	err := svc.Abort(ctx, command.AbortCommand{
		Key:          valueobject.UnsafeIdempotencyKey("nonexistent-key-123456789"),
		Fingerprint:  valueobject.UnsafeFingerprint("sha256:abc"),
		Owner:        valueobject.UnsafeOwner("owner-1"),
		Mode:         model.FailureModeCache,
		ErrorCode:    "ERR",
		ErrorMessage: "test",
		Now:          now,
	})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("Abort(nonexistent) error = %v, want ErrInvalidState", err)
	}
}

// ---- Complete edge cases ----

func TestIdempotencyServiceCompleteRecordNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         valueobject.UnsafeIdempotencyKey("nonexistent-key-123456789"),
		Fingerprint: valueobject.UnsafeFingerprint("sha256:abc"),
		Owner:       valueobject.UnsafeOwner("owner-1"),
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{}`)},
		Now:         now,
	})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("Complete(nonexistent) error = %v, want ErrInvalidState", err)
	}
}

func TestIdempotencyServiceCompleteOwnerMismatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       valueobject.UnsafeOwner("owner-2"), // different owner
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{}`)},
		Now:         now.Add(time.Second),
	})
	if !errors.Is(err, model.ErrOwnerMismatch) {
		t.Fatalf("Complete(wrong owner) error = %v, want ErrOwnerMismatch", err)
	}
}

func TestIdempotencyServiceCompleteFingerprintMismatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         begin.Key,
		Fingerprint: valueobject.UnsafeFingerprint("sha256:different"),
		Owner:       begin.Owner,
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{}`)},
		Now:         now.Add(time.Second),
	})
	if !errors.Is(err, model.ErrFingerprintConflict) {
		t.Fatalf("Complete(wrong fingerprint) error = %v, want ErrFingerprintConflict", err)
	}
}

func TestIdempotencyServiceCompleteNonCacheableResponse(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if begin.Type != dto.BeginResultAcquired {
		t.Fatalf("Begin() type = %s, want %s", begin.Type, dto.BeginResultAcquired)
	}

	// 5xx responses are not cached by default capture rules
	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       begin.Owner,
		Response: dto.CapturedResponse{
			StatusCode: 500,
			Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"error":"internal"}`),
		},
		Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Complete(5xx) error = %v", err)
	}

	// Since 5xx was not cached, the record should have been deleted
	// A new begin with the same key should acquire a fresh record
	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin() after non-cacheable complete type = %s, want %s", second.Type, dto.BeginResultAcquired)
	}
}

// ---- WaitReplay / timeout ----

func TestIdempotencyServiceWaitReplayTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))

	// Use a short wait timeout so the replay loop exits quickly.
	svc := newWaitServiceWithTimeout(t, repo, clock, valueobject.UnsafeOwner("owner-1"), 200*time.Millisecond)

	// Acquire first so a processing record exists.
	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	// Second begin with the same key — the record is still in processing,
	// so the service will wait. The fixed clock's Sleep advances time,
	// which makes the deadline pass. After timeout it retries TryBegin
	// and gets InProgress since the first owner still holds the lock.
	second, err := svc.Begin(ctx, command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers: map[string][]string{
				"Idempotency-Key": []string{"idem-key-123456789"},
			},
			Body: []byte(`{"sku":"A"}`),
		},
	})
	if err != nil {
		t.Fatalf("second Begin() error = %v", err)
	}
	if second.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin() after timeout type = %s, want %s", second.Type, dto.BeginResultInProgress)
	}
}

func TestIdempotencyServiceWaitReplayCompletedWhileWaiting(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))

	// Use wait policy with a long timeout so the immediate Find succeeds.
	svc := newWaitServiceWithTimeout(t, repo, clock, valueobject.UnsafeOwner("owner-1"), 5*time.Second)

	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	// Complete the first record before the second begin.
	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         first.Key,
		Fingerprint: first.Fingerprint,
		Owner:       first.Owner,
		Response:    dto.CapturedResponse{StatusCode: 201, Body: []byte(`{"id":"1"}`)},
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Second begin — the record is now completed, so it should replay immediately.
	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultReplay {
		t.Fatalf("second Begin() after complete type = %s, want %s", second.Type, dto.BeginResultReplay)
	}
	if string(second.Response.Body) != `{"id":"1"}` {
		t.Fatalf("replay body = %q, want %q", string(second.Response.Body), `{"id":"1"}`)
	}
}

// ---- DuplicatePolicy: pass_through ----

func TestIdempotencyServiceDuplicatePassThrough(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newPassThroughService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	first := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	// With pass_through, concurrent requests get InProgress without waiting.
	second := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	if second.Type != dto.BeginResultInProgress {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultInProgress)
	}
}

// ---- Multi-key isolation ----

func TestIdempotencyServiceDifferentKeysDoNotConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	first := beginOrderWithKey(t, svc, ctx, []byte(`{"sku":"A"}`), "key-order-a-123456789")
	if first.Type != dto.BeginResultAcquired {
		t.Fatalf("first Begin() type = %s, want %s", first.Type, dto.BeginResultAcquired)
	}

	second := beginOrderWithKey(t, svc, ctx, []byte(`{"sku":"B"}`), "key-order-b-987654321")
	if second.Type != dto.BeginResultAcquired {
		t.Fatalf("second Begin() type = %s, want %s", second.Type, dto.BeginResultAcquired)
	}

	// Complete both independently
	err := svc.Complete(ctx, command.CompleteCommand{
		Key:         first.Key,
		Fingerprint: first.Fingerprint,
		Owner:       first.Owner,
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{"id":"a"}`)},
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Complete(first) error = %v", err)
	}

	err = svc.Complete(ctx, command.CompleteCommand{
		Key:         second.Key,
		Fingerprint: second.Fingerprint,
		Owner:       second.Owner,
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{"id":"b"}`)},
		Now:         now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Complete(second) error = %v", err)
	}
}

// ---- Error wrapping preserves sentinel errors ----

func TestIdempotencyServiceErrorWrapping(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	svc := newService(t, repo, clock, valueobject.UnsafeOwner("owner-1"))

	// Verify that sentinel errors remain detectable through wrapping.
	_, err := beginOrderNoKey(t, svc, ctx, []byte(`{"sku":"A"}`))
	if err == nil {
		t.Fatal("expected error for missing idempotency key")
	}
	if !errors.Is(err, appservice.ErrMissingIdempotencyKey) {
		t.Fatalf("wrapped error = %v, want ErrMissingIdempotencyKey in chain", err)
	}

	// Verify Complete with wrong owner wraps correctly.
	begin := beginOrder(t, svc, ctx, []byte(`{"sku":"A"}`))
	err = svc.Complete(ctx, command.CompleteCommand{
		Key:         begin.Key,
		Fingerprint: begin.Fingerprint,
		Owner:       valueobject.UnsafeOwner("owner-2"),
		Response:    dto.CapturedResponse{StatusCode: 200, Body: []byte(`{}`)},
		Now:         now.Add(time.Second),
	})
	if !errors.Is(err, model.ErrOwnerMismatch) {
		t.Fatalf("wrapped error = %v, want ErrOwnerMismatch in chain", err)
	}

	// Verify Abort with invalid record wraps correctly.
	err = svc.Abort(ctx, command.AbortCommand{
		Key:          valueobject.UnsafeIdempotencyKey("nonexistent-key-123456789"),
		Fingerprint:  valueobject.UnsafeFingerprint("sha256:abc"),
		Owner:        valueobject.UnsafeOwner("owner-1"),
		Mode:         model.FailureModeCache,
		ErrorCode:    "ERR",
		ErrorMessage: "test",
		Now:          now,
	})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("wrapped abort error = %v, want ErrInvalidState in chain", err)
	}
}

// ---- Helpers ----

func newService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner) *appservice.IdempotencyService {
	t.Helper()
	return newServiceWithPolicy(t, repo, clock, owner, domainservice.DuplicateReject)
}

func newWaitService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner) *appservice.IdempotencyService {
	t.Helper()
	return newServiceWithPolicy(t, repo, clock, owner, domainservice.DuplicateWait)
}

func newPassThroughService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner) *appservice.IdempotencyService {
	t.Helper()
	return newServiceWithPolicy(t, repo, clock, owner, domainservice.DuplicatePassThrough)
}

func newDisabledService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock) *appservice.IdempotencyService {
	t.Helper()
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Disabled:   true,
		Scope:      "order-api",
		Repository: repo,
		Clock:      clock,
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService(disabled) error = %v", err)
	}
	return svc
}

func newWaitServiceWithTimeout(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner, waitTimeout time.Duration) *appservice.IdempotencyService {
	t.Helper()
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Scope:        "order-api",
		Repository:   repo,
		Clock:        clock,
		OwnerFactory: fixedOwnerFactory{owner: owner},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateWait, domainservice.TTLPolicy{
			ProcessingTTL: 30 * time.Second,
			CompletedTTL:  time.Hour,
			FailedTTL:     time.Minute,
		}),
		WaitTimeout:  waitTimeout,
		WaitInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService() error = %v", err)
	}
	return svc
}

func newServiceWithPolicy(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner, duplicatePolicy domainservice.DuplicatePolicy) *appservice.IdempotencyService {
	t.Helper()
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Scope:        "order-api",
		Repository:   repo,
		Clock:        clock,
		OwnerFactory: fixedOwnerFactory{owner: owner},
		Policy: domainservice.NewIdempotencyPolicy(duplicatePolicy, domainservice.TTLPolicy{
			ProcessingTTL: 30 * time.Second,
			CompletedTTL:  time.Hour,
			FailedTTL:     time.Minute,
		}),
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService() error = %v", err)
	}
	return svc
}

func beginOrder(t *testing.T, svc *appservice.IdempotencyService, ctx context.Context, body []byte) dto.BeginResult {
	t.Helper()
	return beginOrderWithKey(t, svc, ctx, body, "idem-key-123456789")
}

func beginOrderWithKey(t *testing.T, svc *appservice.IdempotencyService, ctx context.Context, body []byte, key string) dto.BeginResult {
	t.Helper()
	result, err := svc.Begin(ctx, command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers: map[string][]string{
				"Idempotency-Key": []string{key},
			},
			Body: body,
		},
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return result
}

func beginOrderNoKey(t *testing.T, svc *appservice.IdempotencyService, ctx context.Context, body []byte) (dto.BeginResult, error) {
	t.Helper()
	return svc.Begin(ctx, command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers:   map[string][]string{},
			Body:      body,
		},
	})
}
