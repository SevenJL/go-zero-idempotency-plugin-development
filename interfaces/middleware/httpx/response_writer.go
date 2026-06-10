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
	statusCode    int
	headers       http.Header
	body          bytes.Buffer
	maxBodyBytes  int64
	bodyTruncated bool
	wroteHeader   bool
}

func newCaptureResponseWriter(w http.ResponseWriter, maxBodyBytes int64) *captureResponseWriter {
	return &captureResponseWriter{
		ResponseWriter: w,
		headers:        make(http.Header),
		maxBodyBytes:   maxBodyBytes,
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
	w.captureBody(b)
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
		StatusCode:    code,
		Headers:       headers,
		Body:          copySlice(w.body.Bytes()),
		BodyTruncated: w.bodyTruncated,
	}
}

func (w *captureResponseWriter) captureBody(b []byte) {
	if len(b) == 0 {
		return
	}
	if w.maxBodyBytes <= 0 {
		w.body.Write(b)
		return
	}
	remaining := w.maxBodyBytes - int64(w.body.Len())
	if remaining <= 0 {
		w.bodyTruncated = true
		return
	}
	if int64(len(b)) > remaining {
		w.body.Write(b[:int(remaining)])
		w.bodyTruncated = true
		return
	}
	w.body.Write(b)
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
