package gin

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
)

// ginResponseWriter wraps gin.ResponseWriter to capture the response for replay.
type ginResponseWriter struct {
	gin.ResponseWriter
	statusCode    int
	headers       http.Header
	body          bytes.Buffer
	maxBodyBytes  int64
	bodyTruncated bool
	wroteHeader   bool
}

func newGinResponseWriter(w gin.ResponseWriter, maxBodyBytes int64) *ginResponseWriter {
	return &ginResponseWriter{
		ResponseWriter: w,
		headers:        make(http.Header),
		maxBodyBytes:   maxBodyBytes,
	}
}

func (w *ginResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code

	// Snapshot headers before the first write.
	for k, v := range w.ResponseWriter.Header() {
		w.headers[k] = v
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *ginResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.captureBody(b)
	return w.ResponseWriter.Write(b)
}

func (w *ginResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// CapturedResponse builds a dto.CapturedResponse from the captured data.
func (w *ginResponseWriter) CapturedResponse() dto.CapturedResponse {
	code := w.statusCode
	if code == 0 {
		code = http.StatusOK
	}

	headers := make(map[string][]string, len(w.headers))
	for k, v := range w.headers {
		vs := make([]string, len(v))
		copy(vs, v)
		headers[k] = vs
	}

	body := make([]byte, w.body.Len())
	copy(body, w.body.Bytes())

	return dto.CapturedResponse{
		StatusCode:    code,
		Headers:       headers,
		Body:          body,
		BodyTruncated: w.bodyTruncated,
	}
}

func (w *ginResponseWriter) captureBody(b []byte) {
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
