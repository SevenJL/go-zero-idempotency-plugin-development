package dto

import (
	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type BeginResultType string

const (
	BeginResultAcquired   BeginResultType = "acquired"
	BeginResultReplay     BeginResultType = "replay"
	BeginResultConflict   BeginResultType = "conflict"
	BeginResultInProgress BeginResultType = "in_progress"
	BeginResultFailed     BeginResultType = "failed"
	BeginResultSkipped    BeginResultType = "skipped"
)

type BeginResult struct {
	Type         BeginResultType
	Key          valueobject.IdempotencyKey
	Fingerprint  valueobject.Fingerprint
	Owner        valueobject.Owner
	Record       *model.IdempotencyRecord
	Response     CapturedResponse
	ErrorCode    string
	ErrorMessage string
}

type ReplayResult struct {
	Found        bool
	Key          valueobject.IdempotencyKey
	Record       *model.IdempotencyRecord
	Response     CapturedResponse
	ErrorCode    string
	ErrorMessage string
}
