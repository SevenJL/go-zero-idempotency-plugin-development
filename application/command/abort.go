package command

import (
	"time"

	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/valueobject"
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
