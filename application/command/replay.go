package command

import (
	"time"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/valueobject"
)

type ReplayCommand struct {
	Key      valueobject.IdempotencyKey
	Deadline time.Time
}
