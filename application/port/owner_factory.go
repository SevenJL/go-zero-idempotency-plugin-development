package port

import (
	"context"

	"github.com/your-org/go-idempotency/domain/valueobject"
)

type OwnerFactory interface {
	NewOwner(ctx context.Context) (valueobject.Owner, error)
}
