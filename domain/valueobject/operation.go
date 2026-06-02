package valueobject

import "errors"

var ErrEmptyOperation = errors.New("operation is empty")

type Operation struct {
	value string
}

func NewOperation(value string) (Operation, error) {
	if value == "" {
		return Operation{}, ErrEmptyOperation
	}
	return Operation{value: value}, nil
}

func UnsafeOperation(value string) Operation {
	return Operation{value: value}
}

func (o Operation) String() string {
	return o.value
}

func (o Operation) IsZero() bool {
	return o.value == ""
}
