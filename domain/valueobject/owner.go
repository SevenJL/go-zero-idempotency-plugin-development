package valueobject

import "errors"

var ErrEmptyOwner = errors.New("owner is empty")

type Owner struct {
	value string
}

func NewOwner(value string) (Owner, error) {
	if value == "" {
		return Owner{}, ErrEmptyOwner
	}
	return Owner{value: value}, nil
}

func UnsafeOwner(value string) Owner {
	return Owner{value: value}
}

func (o Owner) String() string {
	return o.value
}

func (o Owner) Equals(other Owner) bool {
	return o.value == other.value
}

func (o Owner) IsZero() bool {
	return o.value == ""
}
