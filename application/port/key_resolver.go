package port

import (
	"context"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type KeyResolver interface {
	Resolve(ctx context.Context, request dto.RequestContext) (valueobject.IdempotencyKey, error)
}
