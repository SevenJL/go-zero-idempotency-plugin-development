package port

import (
	"context"

	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type KeyResolver interface {
	Resolve(ctx context.Context, request dto.RequestContext) (valueobject.IdempotencyKey, error)
}
