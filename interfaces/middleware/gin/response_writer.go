package gin

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/senvejl117/go-idempotency/application/dto"
)

// ginResponseWriter wraps gin.ResponseWriter to capture the response for replay.
type ginResponseWriter struct {
	gin.ResponseWriter
	statusCode  int
	headers     http.Header
	body        bytes.Buffer
	wroteHeader bool
}

func newGinResponseWriter(w gin.ResponseWriter) *ginResponseWriter {
	return &ginResponseWriter{
		ResponseWriter: w,
		headers:        make(http.Header),
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
	w.body.Write(b)
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
		headers[k] = v
	}

	body := make([]byte, w.body.Len())
	copy(body, w.body.Bytes())

	return dto.CapturedResponse{
		StatusCode: code,
		Headers:    headers,
		Body:       body,
	}
}
