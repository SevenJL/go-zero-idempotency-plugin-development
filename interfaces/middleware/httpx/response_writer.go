package httpx

import (
	"bytes"
	"net/http"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
)

// captureResponseWriter wraps http.ResponseWriter to capture the status code,
// headers, and body for idempotency replay.
type captureResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	headers     http.Header
	body        bytes.Buffer
	wroteHeader bool
}

func newCaptureResponseWriter(w http.ResponseWriter) *captureResponseWriter {
	return &captureResponseWriter{
		ResponseWriter: w,
		headers:        make(http.Header),
	}
}

func (w *captureResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code

	// Snapshot headers before the first WriteHeader call.
	copyHeaders(w.headers, w.ResponseWriter.Header())

	w.ResponseWriter.WriteHeader(code)
}

func (w *captureResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	// Capture body for replay.
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Unwrap returns the underlying ResponseWriter. This implements the
// http.ResponseWriter.Unwrap interface (Go 1.20+) for compatibility
// with other middleware that introspects the response writer chain.
func (w *captureResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// CapturedResponse builds a dto.CapturedResponse from the captured data.
func (w *captureResponseWriter) CapturedResponse() dto.CapturedResponse {
	code := w.statusCode
	if code == 0 {
		code = http.StatusOK
	}

	headers := make(map[string][]string, len(w.headers))
	for k, v := range w.headers {
		headers[k] = v
	}

	return dto.CapturedResponse{
		StatusCode: code,
		Headers:    headers,
		Body:       copySlice(w.body.Bytes()),
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		dst[k] = copyStringSlice(vs)
	}
}

func copySlice(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func copyStringSlice(vs []string) []string {
	if len(vs) == 0 {
		return nil
	}
	out := make([]string, len(vs))
	copy(out, vs)
	return out
}
