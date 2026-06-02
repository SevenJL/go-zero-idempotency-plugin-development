package port

import (
	"context"

	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type OwnerFactory interface {
	NewOwner(ctx context.Context) (valueobject.Owner, error)
}
