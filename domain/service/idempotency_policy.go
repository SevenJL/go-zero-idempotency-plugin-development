package service

import "time"

type DuplicatePolicy string

const (
	DuplicateReject      DuplicatePolicy = "reject"
	DuplicateWait        DuplicatePolicy = "wait"
	DuplicatePassThrough DuplicatePolicy = "pass_through"
)

type StorageFailureMode string

const (
	StorageFailureFailClosed StorageFailureMode = "fail_closed"
	StorageFailureFailOpen   StorageFailureMode = "fail_open"
)

type TTLPolicy struct {
	ProcessingTTL time.Duration
	CompletedTTL  time.Duration
	FailedTTL     time.Duration
}

func DefaultTTLPolicy() TTLPolicy {
	return TTLPolicy{
		ProcessingTTL: 30 * time.Second,
		CompletedTTL:  24 * time.Hour,
		FailedTTL:     5 * time.Minute,
	}
}

type IdempotencyPolicy struct {
	DuplicatePolicy DuplicatePolicy
	TTL             TTLPolicy
}

func NewIdempotencyPolicy(duplicate DuplicatePolicy, ttl TTLPolicy) IdempotencyPolicy {
	if duplicate == "" {
		duplicate = DuplicateReject
	}
	if ttl.ProcessingTTL <= 0 {
		ttl.ProcessingTTL = DefaultTTLPolicy().ProcessingTTL
	}
	if ttl.CompletedTTL <= 0 {
		ttl.CompletedTTL = DefaultTTLPolicy().CompletedTTL
	}
	if ttl.FailedTTL <= 0 {
		ttl.FailedTTL = DefaultTTLPolicy().FailedTTL
	}

	return IdempotencyPolicy{
		DuplicatePolicy: duplicate,
		TTL:             ttl,
	}
}

func (p IdempotencyPolicy) ShouldWaitDuplicate() bool {
	return p.DuplicatePolicy == DuplicateWait
}
