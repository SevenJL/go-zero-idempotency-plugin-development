package dto

import "github.com/SevenJL/go-zero-idempotency-plugin-development/domain/valueobject"

type RequestContext struct {
	Operation valueobject.Operation
	Scope     valueobject.Scope
	Headers   map[string][]string
	Metadata  map[string][]string
	Body      []byte
}
