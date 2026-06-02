package command

import (
	"time"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/dto"
)

type BeginCommand struct {
	Request dto.RequestContext
	Now     time.Time
}
