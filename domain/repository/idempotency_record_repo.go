package repository

import (
	"context"
	"errors"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

var ErrStoragePassThrough = errors.New("idempotency storage unavailable: pass through")

type IdempotencyRecordRepository interface {
	TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error)
	Commit(ctx context.Context, record *model.IdempotencyRecord) error
	Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error
	Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error)

	// Renew extends the TTL of a processing record. It must fail when the
	// record does not exist, is not in processing state, or the owner does
	// not match. Implementations may choose to make this a best-effort
	// operation; callers treat errors as non-fatal.
	Renew(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, ttl time.Duration) error
}

type ScopedIdempotencyRecordRepository interface {
	FindScoped(ctx context.Context, key valueobject.IdempotencyKey, scope valueobject.Scope) (*model.IdempotencyRecord, error)
	AbortScoped(ctx context.Context, key valueobject.IdempotencyKey, scope valueobject.Scope, owner valueobject.Owner, mode model.FailureMode) error
	RenewScoped(ctx context.Context, key valueobject.IdempotencyKey, scope valueobject.Scope, owner valueobject.Owner, ttl time.Duration) error
}

func Find(ctx context.Context, repo IdempotencyRecordRepository, key valueobject.IdempotencyKey, scope valueobject.Scope) (*model.IdempotencyRecord, error) {
	if !scope.IsZero() {
		if scoped, ok := repo.(ScopedIdempotencyRecordRepository); ok {
			return scoped.FindScoped(ctx, key, scope)
		}
	}
	return repo.Find(ctx, key)
}

func Abort(ctx context.Context, repo IdempotencyRecordRepository, key valueobject.IdempotencyKey, scope valueobject.Scope, owner valueobject.Owner, mode model.FailureMode) error {
	if !scope.IsZero() {
		if scoped, ok := repo.(ScopedIdempotencyRecordRepository); ok {
			return scoped.AbortScoped(ctx, key, scope, owner, mode)
		}
	}
	return repo.Abort(ctx, key, owner, mode)
}

func Renew(ctx context.Context, repo IdempotencyRecordRepository, key valueobject.IdempotencyKey, scope valueobject.Scope, owner valueobject.Owner, ttl time.Duration) error {
	if !scope.IsZero() {
		if scoped, ok := repo.(ScopedIdempotencyRecordRepository); ok {
			return scoped.RenewScoped(ctx, key, scope, owner, ttl)
		}
	}
	return repo.Renew(ctx, key, owner, ttl)
}
