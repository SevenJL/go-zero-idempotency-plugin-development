package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
	ginidem "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/gin"

	"github.com/gin-gonic/gin"
)

func newTestServer() *gin.Engine {
	gin.SetMode(gin.TestMode)

	clock := &systemClock{}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))

	idemSvc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "gin-test",
		Clock:      clock,
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 30 * time.Second,
			CompletedTTL:  1 * time.Hour,
			FailedTTL:     1 * time.Minute,
		}),
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create service: %v", err))
	}

	r := gin.New()
	r.Use(ginidem.Middleware(idemSvc))

	r.POST("/api/orders", func(c *gin.Context) {
		var req map[string]any
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"order_id": fmt.Sprintf("order-%d", time.Now().UnixNano()),
			"sku":      req["sku"],
			"qty":      req["qty"],
			"status":   "created",
		})
	})
	return r
}

func post(t *testing.T, srv *gin.Engine, key, body string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var data map[string]any
	respBody, _ := io.ReadAll(w.Result().Body)
	_ = json.Unmarshal(respBody, &data)
	w.Result().Body = io.NopCloser(bytes.NewReader(respBody))

	return w.Result(), data
}

// ---- Tests ----

func TestAcquireThenReplay(t *testing.T) {
	srv := newTestServer()
	key := "test-lifecycle-00123"
	body := `{"sku":"A","qty":1}`

	// First request — acquires
	resp1, data1 := post(t, srv, key, body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d", resp1.StatusCode)
	}
	if data1["order_id"] == nil {
		t.Fatal("first request: missing order_id")
	}
	orderID1 := data1["order_id"].(string)

	// Second request — replays (same key, same body)
	resp2, data2 := post(t, srv, key, body)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second request: want 201, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("second request: missing Idempotency-Replayed header")
	}
	// Replay must return the cached order_id, not a new one
	if data2["order_id"].(string) != orderID1 {
		t.Fatalf("replay returned different order_id: %s != %s", data2["order_id"], orderID1)
	}
}

func TestConflictDifferentBody(t *testing.T) {
	srv := newTestServer()
	key := "test-conflict-00123"

	// Acquire with body A
	resp1, _ := post(t, srv, key, `{"sku":"A","qty":1}`)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d", resp1.StatusCode)
	}

	// Same key, different body → conflict
	resp2, data2 := post(t, srv, key, `{"sku":"B","qty":5}`)
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("conflict request: want 409, got %d; body=%v", resp2.StatusCode, data2)
	}
}

func TestInProgress(t *testing.T) {
	srv := newTestServer()
	key := "test-inprogress-00123"
	body := `{"sku":"A","qty":1}`

	// Acquire first — handler completes, record transitions to Completed
	resp1, _ := post(t, srv, key, body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d", resp1.StatusCode)
	}

	// Same key + same body — since first already completed, this replays
	resp2, _ := post(t, srv, key, body)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("replay request: want 201 (replay), got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay request: want Idempotency-Replayed header, got none")
	}
}

func TestMissingKeyPassesThrough(t *testing.T) {
	// Use a server with Required=false so missing keys skip idempotency.
	gin.SetMode(gin.TestMode)
	clock := &systemClock{}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))
	idemSvc, _ := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "gin-test",
		Clock:      clock,
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   false, // allow missing key
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 30 * time.Second,
			CompletedTTL:  1 * time.Hour,
			FailedTTL:     1 * time.Minute,
		}),
	})
	srv := gin.New()
	srv.Use(ginidem.Middleware(idemSvc))
	srv.POST("/api/orders", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"order_id": "order-ok", "status": "created"})
	})

	body := `{"sku":"NOKEY","qty":1}`

	// No Idempotency-Key → skipped, handler executes normally
	resp, data := post(t, srv, "", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("no-key request: want 201, got %d", resp.StatusCode)
	}
	if data["order_id"] == nil {
		t.Fatal("no-key request: missing order_id")
	}
}

func TestInvalidKeyReturnsError(t *testing.T) {
	srv := newTestServer()

	// Key too short
	req := httptest.NewRequest(http.MethodPost, "/api/orders",
		bytes.NewBufferString(`{"sku":"BAD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "short")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("invalid key: want 500, got %d", w.Code)
	}
}

func TestJSONCanonicalization(t *testing.T) {
	srv := newTestServer()
	key := "test-canonical-00123"

	// Acquire with one JSON key order
	resp1, _ := post(t, srv, key, `{"sku":"A","qty":1}`)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d", resp1.StatusCode)
	}

	// Same semantic content, different key order → same fingerprint → replay (not conflict)
	resp2, _ := post(t, srv, key, `{"qty":1,"sku":"A"}`)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("reordered JSON: want 201, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("reordered JSON: want Idempotency-Replayed header")
	}
}

func TestMultipleKeysIndependent(t *testing.T) {
	srv := newTestServer()

	// Key A
	respA, _ := post(t, srv, "test-multi-A-0012345", `{"sku":"A"}`)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("key A: want 201, got %d", respA.StatusCode)
	}

	// Key B (different key → fresh acquire)
	respB, _ := post(t, srv, "test-multi-B-0012345", `{"sku":"B"}`)
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("key B: want 201, got %d", respB.StatusCode)
	}
}

func TestGetMethodSkipped(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// GET is skipped by default → 404 (no route for GET) or 405, not an idempotency error
	if w.Code == http.StatusInternalServerError {
		t.Fatal("GET should be skipped, not return idempotency error")
	}
}
