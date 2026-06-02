// Package gozero provides a go-zero HTTP middleware adapter for the
// idempotency plugin. It wraps the framework-agnostic httpx middleware
// as a go-zero rest.Middleware.
package gozero

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"

	"github.com/SevenJL/go-zero-idempotency-plugin-development/application/service"
	"github.com/SevenJL/go-zero-idempotency-plugin-development/interfaces/middleware/httpx"
)

// Middleware returns a go-zero rest.Middleware that can be used with
// server.Use() or per-route middleware declarations.
//
// Global usage:
//
//	server.Use(gozero.Middleware(idemSvc))
//
// Per-route usage in .api file:
//
//	@server(middleware: Idempotency)
//
// Then register in ServiceContext.
func Middleware(svc *service.IdempotencyService, opts ...httpx.Option) rest.Middleware {
	h := httpx.Middleware(svc, opts...)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			h(next).ServeHTTP(w, r)
		}
	}
}
