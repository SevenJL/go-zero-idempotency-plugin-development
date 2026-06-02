package port

import (
	"context"

	"github.com/senvejl117/go-idempotency/application/dto"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type Fingerprinter interface {
	Fingerprint(ctx context.Context, request dto.RequestContext) (valueobject.Fingerprint, error)
}
