package command

import (
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type CompleteCommand struct {
	Key         valueobject.IdempotencyKey
	Fingerprint valueobject.Fingerprint
	Owner       valueobject.Owner
	Scope       valueobject.Scope
	Response    dto.CapturedResponse
	Now         time.Time
}
