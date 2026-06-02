package repository

import (
	"context"
	"time"

	"github.com/senvejl117/go-idempotency/domain/model"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type IdempotencyRecordRepository interface {
	TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error)
	Commit(ctx context.Context, record *model.IdempotencyRecord) error
	Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error
	Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error)

	// Renew extends the TTL of a processing record. It must fail when the
	// record does not exist, is not in processing state, or the owner does
	// not match. Implementations may choose to make this a best-effort
	// operation — callers treat errors as non-fatal.
	Renew(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, ttl time.Duration) error
}
