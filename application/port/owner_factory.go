package port

import (
	"context"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type OwnerFactory interface {
	NewOwner(ctx context.Context) (valueobject.Owner, error)
}
