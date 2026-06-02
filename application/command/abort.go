package command

import (
	"time"

	"github.com/senvejl117/go-idempotency/application/dto"
	"github.com/senvejl117/go-idempotency/domain/model"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

type AbortCommand struct {
	Key          valueobject.IdempotencyKey
	Fingerprint  valueobject.Fingerprint
	Owner        valueobject.Owner
	Mode         model.FailureMode
	ErrorCode    string
	ErrorMessage string
	Response     dto.CapturedResponse
	Now          time.Time
}
