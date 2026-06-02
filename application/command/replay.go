package command

import (
	"time"

	"github.com/your-org/go-idempotency/domain/valueobject"
)

type ReplayCommand struct {
	Key      valueobject.IdempotencyKey
	Deadline time.Time
}
