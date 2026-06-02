package port

import (
	"context"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type Fingerprinter interface {
	Fingerprint(ctx context.Context, request dto.RequestContext) (valueobject.Fingerprint, error)
}
