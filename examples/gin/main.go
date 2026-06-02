// Example Gin application demonstrating the idempotency plugin.
//
// Start the server:
//
//	go run .
//
// Open http://localhost:8080/ to access the test UI.
package main

import (
	"fmt"
	"log"
	"net/http"
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
	r := gin.Default()

	// Serve the test UI
	r.StaticFile("/", "./static/index.html")
	r.Static("/static", "./static")

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

	fmt.Println("Server starting on http://localhost:8080")
	fmt.Println("Open http://localhost:8080/ in your browser to test idempotency.")
	log.Fatal(r.Run(":8080"))
}

type systemClock struct{}

func (systemClock) Now() time.Time        { return time.Now().UTC() }
func (systemClock) Sleep(d time.Duration) { time.Sleep(d) }
