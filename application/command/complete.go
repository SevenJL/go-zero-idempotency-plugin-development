package command

import (
	"time"

	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type CompleteCommand struct {
	Key         valueobject.IdempotencyKey
	Fingerprint valueobject.Fingerprint
	Owner       valueobject.Owner
	Response    dto.CapturedResponse
	Now         time.Time
}
