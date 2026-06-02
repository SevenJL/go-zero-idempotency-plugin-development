package valueobject

import (
	"errors"
	"regexp"
)

const (
	DefaultMinKeyLength = 16
	DefaultMaxKeyLength = 128
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

var (
	ErrEmptyIdempotencyKey   = errors.New("idempotency key is empty")
	ErrInvalidIdempotencyKey = errors.New("idempotency key is invalid")
)

type IdempotencyKey struct {
	value string
}

func NewIdempotencyKey(value string) (IdempotencyKey, error) {
	return NewIdempotencyKeyWithLimits(value, DefaultMinKeyLength, DefaultMaxKeyLength)
}

func NewIdempotencyKeyWithLimits(value string, minLength, maxLength int) (IdempotencyKey, error) {
	if value == "" {
		return IdempotencyKey{}, ErrEmptyIdempotencyKey
	}
	if len(value) < minLength || len(value) > maxLength || !keyPattern.MatchString(value) {
		return IdempotencyKey{}, ErrInvalidIdempotencyKey
	}
	return IdempotencyKey{value: value}, nil
}

func UnsafeIdempotencyKey(value string) IdempotencyKey {
	return IdempotencyKey{value: value}
}

func (k IdempotencyKey) String() string {
	return k.value
}

func (k IdempotencyKey) IsZero() bool {
	return k.value == ""
}
