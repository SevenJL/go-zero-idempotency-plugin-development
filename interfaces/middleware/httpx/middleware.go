// Package httpx provides a standard net/http middleware for the idempotency
// plugin. It is framework-agnostic and serves as the base for go-zero and Gin
// adapters.
package httpx

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

// Option configures the middleware.
type Option func(*options)

type options struct {
	skipMethods     map[string]bool
	heartbeatConfig *appservice.HeartbeatConfig
	maxBodyBytes    int64
	logger          port.Logger
}

func newOptions() *options {
	return &options{
		skipMethods: map[string]bool{
			http.MethodGet:     true,
			http.MethodHead:    true,
			http.MethodOptions: true,
		},
		maxBodyBytes: 1 << 20, // 1 MB default
		logger:       port.NoopLogger(),
	}
}

// WithSkipMethods sets the HTTP methods that should bypass idempotency
// checks. By default GET, HEAD, and OPTIONS are skipped.
func WithSkipMethods(methods ...string) Option {
	return func(o *options) {
		o.skipMethods = make(map[string]bool)
		for _, m := range methods {
			o.skipMethods[m] = true
		}
	}
}

// WithHeartbeat enables automatic TTL renewal for long-running handlers.
func WithHeartbeat(cfg appservice.HeartbeatConfig) Option {
	return func(o *options) {
		cfgCopy := cfg
		o.heartbeatConfig = &cfgCopy
	}
}

// WithMaxBodyBytes limits the request body size read for fingerprint
// computation. Requests exceeding this limit are rejected with 413.
// Defaults to 1 MB. Set to 0 to disable the limit.
func WithMaxBodyBytes(n int64) Option {
	return func(o *options) {
		o.maxBodyBytes = n
	}
}

// WithLogger sets the logger for the middleware. If not set, a no-op logger
// is used (errors are silently discarded).
func WithLogger(logger port.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// Middleware returns a standard net/http middleware.
//
// Usage:
//
//	mux := http.NewServeMux()
//	mux.Handle("/api/", httpx.Middleware(idemSvc)(myHandler))
func Middleware(svc *appservice.IdempotencyService, opts ...Option) func(http.Handler) http.Handler {
	o := newOptions()
	for _, opt := range opts {
		opt(o)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip read-only methods by default.
			if o.skipMethods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}

			// Read body for fingerprint, then restore.
			// Use a bounded reader to prevent OOM from oversized request bodies.
			var bodyBytes []byte
			if r.Body != nil {
				maxBytes := o.maxBodyBytes
				if maxBytes <= 0 {
					maxBytes = 1 << 20 // fallback default
				}
				limited := io.LimitReader(r.Body, maxBytes+1) // +1 to detect overflow
				bodyBytes, _ = io.ReadAll(limited)
				if int64(len(bodyBytes)) > maxBytes {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			reqCtx := dto.RequestContext{
				Operation: valueobject.UnsafeOperation(r.Method + " " + r.URL.Path),
				Headers:   r.Header,
				Body:      bodyBytes,
			}

		beginResult, err := svc.Begin(r.Context(), command.BeginCommand{Request: reqCtx})
		if err != nil {
			o.logger.Error(r.Context(), "idempotency begin error",
				port.Field{Key: "error", Value: err.Error()},
				port.Field{Key: "method", Value: r.Method},
				port.Field{Key: "path", Value: r.URL.Path},
			)
			http.Error(w, "idempotency error", http.StatusInternalServerError)
			return
		}

			switch beginResult.Type {
			case dto.BeginResultSkipped:
				next.ServeHTTP(w, r)

			case dto.BeginResultAcquired:
				var hb *appservice.Heartbeat
				ctx := r.Context()
				if o.heartbeatConfig != nil {
					cfg := *o.heartbeatConfig
					cfg.Key = beginResult.Key
					cfg.Owner = beginResult.Owner
					hb = appservice.NewHeartbeat(cfg)
					ctx = hb.Start(ctx)
				}

				crw := newCaptureResponseWriter(w)
				if hb != nil {
					defer hb.Stop()
				}
				next.ServeHTTP(crw, r.WithContext(ctx))

				resp := crw.CapturedResponse()
				_ = svc.Complete(ctx, command.CompleteCommand{
					Key:         beginResult.Key,
					Fingerprint: beginResult.Fingerprint,
					Owner:       beginResult.Owner,
					Response:    resp,
				})

			case dto.BeginResultReplay:
				writeReplayResponse(w, beginResult)

			case dto.BeginResultConflict:
				http.Error(w, `{"error":"idempotency: fingerprint conflict"}`, http.StatusConflict)

			case dto.BeginResultInProgress:
				http.Error(w, `{"error":"idempotency: request in progress"}`, http.StatusConflict)

			case dto.BeginResultFailed:
				writeReplayResponse(w, beginResult)
			}
		})
	}
}

func writeReplayResponse(w http.ResponseWriter, result dto.BeginResult) {
	w.Header().Set("Idempotency-Replayed", "true")
	for k, vals := range result.Response.Headers {
		// Skip excluded headers that may have leaked through.
		if isExcludedReplayHeader(k) {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if result.Response.StatusCode != 0 {
		w.WriteHeader(result.Response.StatusCode)
	}
	if len(result.Response.Body) > 0 {
		_, _ = w.Write(result.Response.Body)
	}
}

func isExcludedReplayHeader(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "set-cookie", "authorization", "cookie", "www-authenticate":
		return true
	}
	return false
}
