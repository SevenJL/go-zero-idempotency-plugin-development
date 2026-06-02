package command

import (
	"time"

	"github.com/senvejl117/go-idempotency/application/dto"
)

type BeginCommand struct {
	Request dto.RequestContext
	Now     time.Time
}
