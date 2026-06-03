// Example go-zero HTTP application demonstrating the idempotency plugin.
//
// Start the server:
//
//	go run .
//
// Test with curl:
//
//	# Acquire
//	curl -s -X POST http://localhost:8888/api/orders \
//	  -H "Content-Type: application/json" \
//	  -H "Idempotency-Key: test-key-0000000001" \
//	  -d '{"sku":"test","qty":1}'
//
//	# Replay (same key)
//	curl -s -X POST http://localhost:8888/api/orders \
//	  -H "Content-Type: application/json" \
//	  -H "Idempotency-Key: test-key-0000000001" \
//	  -d '{"sku":"test","qty":1}'
//
//	# Conflict (different body, same key)
//	curl -s -X POST http://localhost:8888/api/orders \
//	  -H "Content-Type: application/json" \
//	  -H "Idempotency-Key: test-key-0000000001" \
//	  -d '{"sku":"evil","qty":999}'
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
	gozerohttp "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/gozero"
)

func main() {
	// ---- Build the idempotency service ----
	repo := memory.NewIdempotencyRecordRepository()
	idemSvc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "gozero-http-example",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   false,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create idempotency service: %v", err)
	}

	// ---- Set up go-zero HTTP server ----
	// In a real project, you would load this from a YAML config file.
	server := rest.MustNewServer(rest.RestConf{
		Host: "0.0.0.0",
		Port: 8888,
		Timeout: 30000,
		MaxConns: 10000,
	},
		rest.WithNotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		})),
	)
	defer server.Stop()

	// Apply idempotency middleware globally
	server.Use(gozerohttp.Middleware(idemSvc))

	// Register business routes
	server.AddRoutes([]rest.Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/orders",
			Handler: createOrder,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/payments",
			Handler: createPayment,
		},
	})

	// Health check
	server.AddRoute(rest.Route{
		Method: http.MethodGet,
		Path:   "/health",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		},
	})

	fmt.Println("go-zero HTTP server starting on http://localhost:8888")
	fmt.Println("Endpoints:")
	fmt.Println("  POST /api/orders    — idempotency-protected")
	fmt.Println("  POST /api/payments  — idempotency-protected")
	fmt.Println("  GET  /health        — health check")

	// ---- Graceful shutdown ----
	go func() {
		server.Start()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down gracefully...")
	server.Stop()
	fmt.Println("Server stopped.")
}

// ---- Business handlers ----

func createOrder(w http.ResponseWriter, r *http.Request) {
	logx.WithContext(r.Context()).Infow("creating order")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(fmt.Sprintf(
		`{"order_id":"order-%d","status":"created"}`,
		time.Now().UnixNano(),
	)))
}

func createPayment(w http.ResponseWriter, r *http.Request) {
	logx.WithContext(r.Context()).Infow("processing payment")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(
		`{"payment_id":"pay-%d","status":"completed"}`,
		time.Now().UnixNano(),
	)))
}

// Ensure unused import is fine for compilation check
var _ = context.Background
