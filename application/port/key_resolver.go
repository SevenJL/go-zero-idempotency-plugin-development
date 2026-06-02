package port

import (
	"context"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/dto"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/valueobject"
)

type KeyResolver interface {
	Resolve(ctx context.Context, request dto.RequestContext) (valueobject.IdempotencyKey, error)
}
