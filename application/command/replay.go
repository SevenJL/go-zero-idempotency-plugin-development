package command

import (
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type ReplayCommand struct {
	Key      valueobject.IdempotencyKey
	Deadline time.Time
}
