package command

import (
	"time"

	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type ReplayCommand struct {
	Key      valueobject.IdempotencyKey
	Deadline time.Time
}
