// Package gin provides a Gin middleware adapter for the idempotency plugin.
package gin

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/your-org/go-idempotency/application/command"
	"github.com/your-org/go-idempotency/application/dto"
	appservice "github.com/your-org/go-idempotency/application/service"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

// Middleware returns a gin.HandlerFunc that provides idempotency protection.
//
// Usage:
//
//	r := gin.New()
//	r.Use(ginidem.Middleware(idemSvc))
func Middleware(svc *appservice.IdempotencyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip read-only methods.
		if isReadOnlyMethod(c.Request.Method) {
			c.Next()
			return
		}

		// Read body for fingerprint, then restore so the handler can read it.
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
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
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		switch beginResult.Type {
		case dto.BeginResultSkipped:
			c.Next()

		case dto.BeginResultAcquired:
			// Wrap the response writer to capture the response.
			crw := newGinResponseWriter(c.Writer)
			c.Writer = crw

			c.Next()

			// If the handler aborted, the captured response may be incomplete.
			// We still complete to prevent indefinite processing state.
			resp := crw.CapturedResponse()
			_ = svc.Complete(c.Request.Context(), command.CompleteCommand{
				Key:         beginResult.Key,
				Fingerprint: beginResult.Fingerprint,
				Owner:       beginResult.Owner,
				Response:    resp,
			})

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
	if result.Response.StatusCode != 0 {
		c.AbortWithStatusJSON(result.Response.StatusCode, result.Response.Body)
		return
	}
	if len(result.Response.Body) > 0 {
		c.AbortWithStatusJSON(http.StatusOK, result.Response.Body)
		return
	}
	c.AbortWithStatus(http.StatusOK)
}

func isReadOnlyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func isExcludedReplayHeader(name string) bool {
	switch strings.ToLower(name) {
	case "set-cookie", "authorization", "cookie", "www-authenticate":
		return true
	}
	return false
}
