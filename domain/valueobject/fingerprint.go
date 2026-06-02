package valueobject

import "errors"

var ErrEmptyFingerprint = errors.New("fingerprint is empty")

type Fingerprint struct {
	value string
}

func NewFingerprint(value string) (Fingerprint, error) {
	if value == "" {
		return Fingerprint{}, ErrEmptyFingerprint
	}
	return Fingerprint{value: value}, nil
}

func UnsafeFingerprint(value string) Fingerprint {
	return Fingerprint{value: value}
}

func (f Fingerprint) String() string {
	return f.value
}

func (f Fingerprint) Equals(other Fingerprint) bool {
	return f.value == other.value
}

func (f Fingerprint) IsZero() bool {
	return f.value == ""
}
