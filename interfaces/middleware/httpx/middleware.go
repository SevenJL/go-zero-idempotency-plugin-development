// Package httpx provides a standard net/http middleware for the idempotency
// plugin. It is framework-agnostic and serves as the base for go-zero and Gin
// adapters.
package httpx

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/your-org/go-idempotency/application/command"
	"github.com/your-org/go-idempotency/application/dto"
	appservice "github.com/your-org/go-idempotency/application/service"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

// Option configures the middleware.
type Option func(*options)

type options struct {
	skipMethods     map[string]bool
	heartbeatConfig *appservice.HeartbeatConfig
}

func newOptions() *options {
	return &options{
		skipMethods: map[string]bool{
			http.MethodGet:     true,
			http.MethodHead:    true,
			http.MethodOptions: true,
		},
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
			var bodyBytes []byte
			if r.Body != nil {
				bodyBytes, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			reqCtx := dto.RequestContext{
				Operation: valueobject.UnsafeOperation(r.Method + " " + r.URL.Path),
				Headers:   r.Header,
				Body:      bodyBytes,
			}

			beginResult, err := svc.Begin(r.Context(), command.BeginCommand{Request: reqCtx})
			if err != nil {
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
				next.ServeHTTP(crw, r.WithContext(ctx))

				if hb != nil {
					hb.Stop()
				}

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
