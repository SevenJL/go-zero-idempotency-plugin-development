package command

import (
	"time"

	"github.com/senvejl117/go-idempotency/application/dto"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type CompleteCommand struct {
	Key         valueobject.IdempotencyKey
	Fingerprint valueobject.Fingerprint
	Owner       valueobject.Owner
	Response    dto.CapturedResponse
	Now         time.Time
}
