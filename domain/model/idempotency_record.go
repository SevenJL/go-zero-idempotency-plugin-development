package model

import (
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type IdempotencyRecord struct {
	key         valueobject.IdempotencyKey
	fingerprint valueobject.Fingerprint
	owner       valueobject.Owner
	operation   valueobject.Operation
	scope       valueobject.Scope
	status      IdempotencyStatus
	response    CapturedResponse
	errCode     string
	errMessage  string
	createdAt   time.Time
	updatedAt   time.Time
	expiresAt   time.Time
}

type NewRecordParams struct {
	Key           valueobject.IdempotencyKey
	Fingerprint   valueobject.Fingerprint
	Owner         valueobject.Owner
	Operation     valueobject.Operation
	Scope         valueobject.Scope
	Now           time.Time
	ProcessingTTL time.Duration
}

func NewProcessingRecord(params NewRecordParams) (*IdempotencyRecord, error) {
	if params.Key.IsZero() {
		return nil, valueobject.ErrEmptyIdempotencyKey
	}
	if params.Fingerprint.IsZero() {
		return nil, valueobject.ErrEmptyFingerprint
	}
	if params.Owner.IsZero() {
		return nil, valueobject.ErrEmptyOwner
	}
	if params.Operation.IsZero() {
		return nil, valueobject.ErrEmptyOperation
	}
	if params.ProcessingTTL <= 0 {
		return nil, ErrInvalidTTL
	}

	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	return &IdempotencyRecord{
		key:         params.Key,
		fingerprint: params.Fingerprint,
		owner:       params.Owner,
		operation:   params.Operation,
		scope:       params.Scope,
		status:      StatusProcessing,
		createdAt:   now,
		updatedAt:   now,
		expiresAt:   now.Add(params.ProcessingTTL),
	}, nil
}

func RestoreRecord(params RestoreRecordParams) *IdempotencyRecord {
	return &IdempotencyRecord{
		key:         params.Key,
		fingerprint: params.Fingerprint,
		owner:       params.Owner,
		operation:   params.Operation,
		scope:       params.Scope,
		status:      params.Status,
		response:    params.Response.Clone(),
		errCode:     params.ErrorCode,
		errMessage:  params.ErrorMessage,
		createdAt:   params.CreatedAt,
		updatedAt:   params.UpdatedAt,
		expiresAt:   params.ExpiresAt,
	}
}

type RestoreRecordParams struct {
	Key          valueobject.IdempotencyKey
	Fingerprint  valueobject.Fingerprint
	Owner        valueobject.Owner
	Operation    valueobject.Operation
	Scope        valueobject.Scope
	Status       IdempotencyStatus
	Response     CapturedResponse
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

func (r *IdempotencyRecord) Complete(owner valueobject.Owner, fingerprint valueobject.Fingerprint, response CapturedResponse, now time.Time, completedTTL time.Duration) error {
	if r.status != StatusProcessing {
		return ErrInvalidState
	}
	if !r.owner.Equals(owner) {
		return ErrOwnerMismatch
	}
	if !r.fingerprint.Equals(fingerprint) {
		return ErrFingerprintConflict
	}
	if response.IsEmpty() {
		return ErrResponseNotCacheable
	}
	if completedTTL <= 0 {
		return ErrInvalidTTL
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	r.status = StatusCompleted
	r.response = response.Clone()
	r.updatedAt = now
	r.expiresAt = now.Add(completedTTL)
	return nil
}

func (r *IdempotencyRecord) MarkFailed(owner valueobject.Owner, fingerprint valueobject.Fingerprint, code, message string, response CapturedResponse, now time.Time, failedTTL time.Duration) error {
	if r.status != StatusProcessing {
		return ErrInvalidState
	}
	if !r.owner.Equals(owner) {
		return ErrOwnerMismatch
	}
	if !r.fingerprint.Equals(fingerprint) {
		return ErrFingerprintConflict
	}
	if failedTTL <= 0 {
		return ErrInvalidTTL
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	r.status = StatusFailed
	r.response = response.Clone()
	r.errCode = code
	r.errMessage = message
	r.updatedAt = now
	r.expiresAt = now.Add(failedTTL)
	return nil
}

func (r *IdempotencyRecord) ConflictsWith(fingerprint valueobject.Fingerprint) bool {
	return !r.fingerprint.Equals(fingerprint)
}

func (r *IdempotencyRecord) IsExpired(now time.Time) bool {
	if r.expiresAt.IsZero() {
		return false
	}
	return !now.Before(r.expiresAt)
}

func (r *IdempotencyRecord) Clone() *IdempotencyRecord {
	if r == nil {
		return nil
	}
	return RestoreRecord(RestoreRecordParams{
		Key:          r.key,
		Fingerprint:  r.fingerprint,
		Owner:        r.owner,
		Operation:    r.operation,
		Scope:        r.scope,
		Status:       r.status,
		Response:     r.response,
		ErrorCode:    r.errCode,
		ErrorMessage: r.errMessage,
		CreatedAt:    r.createdAt,
		UpdatedAt:    r.updatedAt,
		ExpiresAt:    r.expiresAt,
	})
}

func (r *IdempotencyRecord) Key() valueobject.IdempotencyKey {
	return r.key
}

func (r *IdempotencyRecord) Fingerprint() valueobject.Fingerprint {
	return r.fingerprint
}

func (r *IdempotencyRecord) Owner() valueobject.Owner {
	return r.owner
}

func (r *IdempotencyRecord) Operation() valueobject.Operation {
	return r.operation
}

func (r *IdempotencyRecord) Scope() valueobject.Scope {
	return r.scope
}

func (r *IdempotencyRecord) Status() IdempotencyStatus {
	return r.status
}

func (r *IdempotencyRecord) Response() CapturedResponse {
	return r.response.Clone()
}

func (r *IdempotencyRecord) ErrorCode() string {
	return r.errCode
}

func (r *IdempotencyRecord) ErrorMessage() string {
	return r.errMessage
}

func (r *IdempotencyRecord) CreatedAt() time.Time {
	return r.createdAt
}

func (r *IdempotencyRecord) UpdatedAt() time.Time {
	return r.updatedAt
}

func (r *IdempotencyRecord) ExpiresAt() time.Time {
	return r.expiresAt
}
