// Package gin provides a Gin middleware adapter for the idempotency plugin.
package gin

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

type Option func(*options)

type options struct {
	skipMethods          map[string]bool
	heartbeatConfig      *appservice.HeartbeatConfig
	maxBodyBytes         int64
	maxResponseBodyBytes int64
}

func newOptions() *options {
	return &options{
		skipMethods: map[string]bool{
			http.MethodGet:     true,
			http.MethodHead:    true,
			http.MethodOptions: true,
		},
		maxBodyBytes:         1 << 20,
		maxResponseBodyBytes: 1 << 20,
	}
}

func WithSkipMethods(methods ...string) Option {
	return func(o *options) {
		o.skipMethods = make(map[string]bool)
		for _, method := range methods {
			o.skipMethods[method] = true
		}
	}
}

func WithHeartbeat(cfg appservice.HeartbeatConfig) Option {
	return func(o *options) {
		cfgCopy := cfg
		o.heartbeatConfig = &cfgCopy
	}
}

func WithMaxBodyBytes(n int64) Option {
	return func(o *options) {
		o.maxBodyBytes = n
	}
}

func WithMaxResponseBodyBytes(n int64) Option {
	return func(o *options) {
		o.maxResponseBodyBytes = n
	}
}

// Middleware returns a gin.HandlerFunc that provides idempotency protection.
//
// Usage:
//
//	r := gin.New()
//	r.Use(ginidem.Middleware(idemSvc))
func Middleware(svc *appservice.IdempotencyService, opts ...Option) gin.HandlerFunc {
	o := newOptions()
	for _, opt := range opts {
		opt(o)
	}

	return func(c *gin.Context) {
		// Skip read-only methods.
		if o.skipMethods[c.Request.Method] {
			c.Next()
			return
		}

		// Read body for fingerprint, then restore so the handler can read it.
		// Use a bounded reader to prevent OOM from oversized request bodies.
		var bodyBytes []byte
		if c.Request.Body != nil {
			maxBytes := o.maxBodyBytes
			reader := io.Reader(c.Request.Body)
			if maxBytes > 0 {
				reader = io.LimitReader(c.Request.Body, maxBytes+1) // +1 to detect overflow
			}
			var err error
			bodyBytes, err = io.ReadAll(reader)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "request body read error"})
				return
			}
			if maxBytes > 0 && int64(len(bodyBytes)) > maxBytes {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Prefer the route pattern (FullPath) for fingerprint stability.
		// Fall back to URL.Path for routes without explicit patterns.
		operation := c.Request.Method + " " + c.Request.URL.Path
		if fp := c.FullPath(); fp != "" {
			operation = c.Request.Method + " " + fp
		}

		reqCtx := dto.RequestContext{
			Operation: valueobject.UnsafeOperation(operation),
			Headers:   c.Request.Header,
			Body:      bodyBytes,
		}

		beginResult, err := svc.Begin(c.Request.Context(), command.BeginCommand{Request: reqCtx})
		if err != nil {
			c.Error(err)
			c.AbortWithStatusJSON(beginErrorStatus(err), gin.H{"error": "idempotency: begin error"})
			return
		}

		switch beginResult.Type {
		case dto.BeginResultSkipped, dto.BeginResultPassThrough:
			c.Next()

		case dto.BeginResultAcquired:
			var hb *appservice.Heartbeat
			ctx := c.Request.Context()
			if o.heartbeatConfig != nil {
				cfg := *o.heartbeatConfig
				cfg.Key = beginResult.Key
				cfg.Scope = beginResult.Scope
				cfg.Owner = beginResult.Owner
				hb = appservice.NewHeartbeat(cfg)
				ctx = hb.Start(ctx)
				c.Request = c.Request.WithContext(ctx)
			}
			if hb != nil {
				defer hb.Stop()
			}

			// Wrap the response writer to capture the response.
			crw := newGinResponseWriter(c.Writer, o.maxResponseBodyBytes)
			c.Writer = crw

			c.Next()

			// If the handler aborted, the captured response may be incomplete.
			// We still complete to prevent indefinite processing state.
			resp := crw.CapturedResponse()
			if err := svc.Complete(c.Request.Context(), command.CompleteCommand{
				Key:         beginResult.Key,
				Fingerprint: beginResult.Fingerprint,
				Owner:       beginResult.Owner,
				Scope:       beginResult.Scope,
				Response:    resp,
			}); err != nil {
				c.Error(err)
			}

		case dto.BeginResultReplay:
			c.Header("Idempotency-Replayed", "true")
			writeReplayGin(c, beginResult)

		case dto.BeginResultConflict:
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "idempotency: fingerprint conflict"})

		case dto.BeginResultInProgress:
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "idempotency: request in progress"})

		case dto.BeginResultFailed:
			c.Header("Idempotency-Replayed", "true")
			writeReplayGin(c, beginResult)
		}
	}
}

func writeReplayGin(c *gin.Context, result dto.BeginResult) {
	for k, vals := range result.Response.Headers {
		if isExcludedReplayHeader(k) {
			continue
		}
		for _, v := range vals {
			c.Header(k, v)
		}
	}
	status := result.Response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	// Use the codec's content type if available, otherwise default to JSON.
	contentType := result.Response.Codec
	if contentType == "" {
		contentType = "application/json"
	}
	if len(result.Response.Body) > 0 {
		c.Data(status, contentType, result.Response.Body)
	} else {
		c.AbortWithStatus(status)
	}
	c.Abort()
}

func isExcludedReplayHeader(name string) bool {
	switch strings.ToLower(name) {
	case "set-cookie", "authorization", "cookie", "www-authenticate":
		return true
	}
	return false
}

func beginErrorStatus(err error) int {
	switch {
	case errors.Is(err, appservice.ErrMissingIdempotencyKey),
		errors.Is(err, valueobject.ErrEmptyIdempotencyKey),
		errors.Is(err, valueobject.ErrInvalidIdempotencyKey):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
