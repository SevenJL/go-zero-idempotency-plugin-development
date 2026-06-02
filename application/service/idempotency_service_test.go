package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/go-idempotency/application/command"
	"github.com/your-org/go-idempotency/application/dto"
	appservice "github.com/your-org/go-idempotency/application/service"
	"github.com/your-org/go-idempotency/domain/model"
	domainservice "github.com/your-org/go-idempotency/domain/service"
	"github.com/your-org/go-idempotency/domain/valueobject"
	"github.com/your-org/go-idempotency/infrastructure/persistence/memory"
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

func newService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner) *appservice.IdempotencyService {
	t.Helper()

	return newServiceWithPolicy(t, repo, clock, owner, domainservice.DuplicateReject)
}

func newWaitService(t *testing.T, repo *memory.IdempotencyRecordRepository, clock *fixedClock, owner valueobject.Owner) *appservice.IdempotencyService {
	t.Helper()

	return newServiceWithPolicy(t, repo, clock, owner, domainservice.DuplicateWait)
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

	result, err := svc.Begin(ctx, command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /orders"),
			Headers: map[string][]string{
				"Idempotency-Key": []string{"idem-key-123456789"},
			},
			Body: body,
		},
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return result
}
