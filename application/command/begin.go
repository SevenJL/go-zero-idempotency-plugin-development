package command

import (
	"time"

	"github.com/your-org/go-idempotency/application/dto"
)

type BeginCommand struct {
	Request dto.RequestContext
	Now     time.Time
}
