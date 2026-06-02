package port

import (
	"context"

	"github.com/senvejl117/go-idempotency/application/dto"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type KeyResolver interface {
	Resolve(ctx context.Context, request dto.RequestContext) (valueobject.IdempotencyKey, error)
}
