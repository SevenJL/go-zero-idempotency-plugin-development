package dto

import "github.com/senvejl117/go-idempotency/domain/valueobject"

type RequestContext struct {
	Operation valueobject.Operation
	Scope     valueobject.Scope
	Headers   map[string][]string
	Metadata  map[string][]string
	Body      []byte
}
