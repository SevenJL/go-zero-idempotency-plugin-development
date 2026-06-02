package command

import (
	"time"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/dto"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/model"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/domain/valueobject"
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
