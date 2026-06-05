// Package gin provides a Gin middleware adapter for the idempotency plugin.
package gin

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

const maxBodyBytes = 1 << 20 // 1 MB

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
		// Use a bounded reader to prevent OOM from oversized request bodies.
		var bodyBytes []byte
		if c.Request.Body != nil {
			limited := io.LimitReader(c.Request.Body, maxBodyBytes+1) // +1 to detect overflow
			bodyBytes, _ = io.ReadAll(limited)
			if int64(len(bodyBytes)) > maxBodyBytes {
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
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "idempotency: internal error"})
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
		if err := svc.Complete(c.Request.Context(), command.CompleteCommand{
			Key:         beginResult.Key,
			Fingerprint: beginResult.Fingerprint,
			Owner:       beginResult.Owner,
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
