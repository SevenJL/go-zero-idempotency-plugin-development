package repository

import (
	"context"

	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type IdempotencyRecordRepository interface {
	TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error)
	Commit(ctx context.Context, record *model.IdempotencyRecord) error
	Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error
	Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error)
}
