package port

import (
	"context"

	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type Fingerprinter interface {
	Fingerprint(ctx context.Context, request dto.RequestContext) (valueobject.Fingerprint, error)
}
