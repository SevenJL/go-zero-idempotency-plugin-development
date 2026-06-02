package model

import "errors"

var (
	ErrInvalidState         = errors.New("idempotency: invalid state")
	ErrOwnerMismatch        = errors.New("idempotency: owner mismatch")
	ErrFingerprintConflict = errors.New("idempotency: fingerprint conflict")
	ErrResponseNotCacheable = errors.New("idempotency: response is not cacheable")
	ErrInvalidTTL           = errors.New("idempotency: invalid ttl")
)
