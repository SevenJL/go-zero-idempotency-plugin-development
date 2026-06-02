package command

import (
	"time"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/dto"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/valueobject"
)

type CompleteCommand struct {
	Key         valueobject.IdempotencyKey
	Fingerprint valueobject.Fingerprint
	Owner       valueobject.Owner
	Response    dto.CapturedResponse
	Now         time.Time
}
