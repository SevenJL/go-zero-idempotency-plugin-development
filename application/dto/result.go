package dto

import (
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type BeginResultType string

const (
	BeginResultAcquired    BeginResultType = "acquired"
	BeginResultReplay      BeginResultType = "replay"
	BeginResultConflict    BeginResultType = "conflict"
	BeginResultInProgress  BeginResultType = "in_progress"
	BeginResultFailed      BeginResultType = "failed"
	BeginResultSkipped     BeginResultType = "skipped"
	BeginResultPassThrough BeginResultType = "pass_through"
)

type BeginResult struct {
	Type         BeginResultType
	Key          valueobject.IdempotencyKey
	Fingerprint  valueobject.Fingerprint
	Owner        valueobject.Owner
	Scope        valueobject.Scope
	Record       *model.IdempotencyRecord
	Response     CapturedResponse
	ErrorCode    string
	ErrorMessage string
}

type ReplayResult struct {
	Found        bool
	Key          valueobject.IdempotencyKey
	Scope        valueobject.Scope
	Record       *model.IdempotencyRecord
	Response     CapturedResponse
	ErrorCode    string
	ErrorMessage string
}
