package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
)

func TestMiddlewareMaxBodyBytesZeroDisablesLimit(t *testing.T) {
	svc := newHTTPXTestService(t)
	handler := Middleware(svc, WithMaxBodyBytes(0))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(strings.Repeat("a", 1<<20+8)))
	req.Header.Set("Idempotency-Key", "httpx-key-0000000001")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%q", resp.Code, http.StatusCreated, resp.Body.String())
	}
}

func TestMiddlewareRejectsBodyAboveConfiguredLimit(t *testing.T) {
	svc := newHTTPXTestService(t)
	handler := Middleware(svc, WithMaxBodyBytes(4))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for oversized body")
	}))

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("12345"))
	req.Header.Set("Idempotency-Key", "httpx-key-0000000002")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusRequestEntityTooLarge)
	}
}

func newHTTPXTestService(t *testing.T) *appservice.IdempotencyService {
	t.Helper()
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Scope:        "httpx-test",
		Repository:   memory.NewIdempotencyRecordRepository(),
		OwnerFactory: fixedOwnerFactory{owner: valueobject.UnsafeOwner("owner-1")},
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}
	return svc
}

type fixedOwnerFactory struct {
	owner valueobject.Owner
}

func (f fixedOwnerFactory) NewOwner(_ context.Context) (valueobject.Owner, error) {
	return f.owner, nil
}
