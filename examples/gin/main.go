// Example Gin application demonstrating the idempotency plugin.
//
// Start the server:
//
//	go run .
//
// Open http://localhost:8080/ to access the test UI.
//
// Health & readiness:
//
//	curl http://localhost:8080/health
//	curl http://localhost:8080/ready
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
	ginidem "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/gin"
)

func main() {
	// ---- Build the idempotency service ----
	clock := &systemClock{}
	repo := memory.NewIdempotencyRecordRepository(memory.WithClock(clock.Now))

	idemSvc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "gin-example",
		Clock:      clock,
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "Idempotency-Key",
			Required:   false, // allow requests without key to pass through
		},
		Policy: domainservice.NewIdempotencyPolicy(domainservice.DuplicateReject, domainservice.TTLPolicy{
			ProcessingTTL: 30 * time.Second,
			CompletedTTL:  1 * time.Hour,
			FailedTTL:     1 * time.Minute,
		}),
	})
	if err != nil {
		log.Fatalf("Failed to create idempotency service: %v", err)
	}

	// ---- Set up Gin ----
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Serve the test UI
	r.StaticFile("/", "./static/index.html")
	r.Static("/static", "./static")

	// ---- Debug/pprof (production profiling) ----
	pprofGroup := r.Group("/debug/pprof")
	pprofGroup.GET("/", gin.WrapF(pprof.Index))
	pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
	pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
	pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
	pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
	pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
	pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
	pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))

	// ---- Health & Readiness endpoints ----
	// Health: always returns 200 while process is alive.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Readiness: verifies that the idempotency service is operational.
	r.GET("/ready", func(c *gin.Context) {
		// The memory repository is always ready; for Redis-based deployments
		// you would add a PING check here.
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Apply idempotency middleware globally (only affects POST/PUT/PATCH/DELETE)
	r.Use(ginidem.Middleware(idemSvc))

	// Business endpoint — protected by idempotency middleware
	r.POST("/api/orders", func(c *gin.Context) {
		// Simulate business logic
		var req map[string]any
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		orderID := fmt.Sprintf("order-%d", time.Now().UnixNano())
		c.JSON(http.StatusCreated, gin.H{
			"order_id": orderID,
			"sku":      req["sku"],
			"qty":      req["qty"],
			"status":   "created",
		})
	})

	// ---- HTTP Server with graceful shutdown ----
	addr := ":8080"
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background
	go func() {
		fmt.Printf("Server starting on http://localhost%s\n", addr)
		fmt.Println("Open http://localhost:8080/ in your browser to test idempotency.")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// ---- Graceful shutdown ----
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	fmt.Println("Server stopped.")
}

type systemClock struct{}

func (systemClock) Now() time.Time        { return time.Now().UTC() }
func (systemClock) Sleep(d time.Duration) { time.Sleep(d) }
