# =============================================================================
# Makefile for go-zero idempotency plugin
#
# Targets:
#   make test              Run unit + integration tests with coverage
#   make test-integration  Run Redis integration tests (requires Redis)
#   make test-all          Run all tests (unit + integration + Redis + examples)
#   make lint              Run golangci-lint
#   make build             Build the Gin example binary
#   make docker-build      Build Docker image
#   make docker-smoke      Build and smoke-test Docker image
#   make bench             Run benchmark suite
#   make cover             Generate HTML coverage report
#   make cover-check       Check coverage against threshold
#   make tidy              Tidy all Go modules
#   make fmt               Format all Go code
#   make clean             Remove build artifacts
# =============================================================================

APP_NAME    := idempotency-example
GO          := go
GOFLAGS     := -trimpath
LDFLAGS     := -s -w
COVER_FILE  := coverage.out
COVER_HTML  := coverage.html
COVER_THRESHOLD := 50

.PHONY: test test-integration test-all lint build docker-build docker-smoke bench cover cover-check tidy fmt clean

# ---- Test ----

test:
	$(GO) test ./... -count=1 -coverprofile=$(COVER_FILE) -covermode=atomic -timeout=120s

test-integration:
	$(GO) test ./tests/ -count=1 -tags=integration -run Redis -timeout=60s -v

test-examples:
	cd examples/gin && $(GO) test ./... -count=1 -timeout=60s -v
	cd examples/gozero-http && $(GO) test ./... -count=1 -timeout=60s -v || true

test-all: test test-integration test-examples

# ---- Lint ----

lint:
	golangci-lint run ./... --timeout=5m

# ---- Build ----

build:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME) ./examples/gin/

build-all:
	$(GO) build $(GOFLAGS) ./...

# ---- Docker ----

docker-build:
	docker build -t $(APP_NAME):latest .

docker-smoke: docker-build
	docker run -d --rm -p 8080:8080 --name $(APP_NAME)-smoke $(APP_NAME):latest
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if curl -sf http://localhost:8080/health > /dev/null 2>&1; then \
			echo "Container healthy after $${i}s"; \
			docker stop $(APP_NAME)-smoke; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "Container failed to become healthy"; \
	docker logs $(APP_NAME)-smoke; \
	docker stop $(APP_NAME)-smoke; \
	exit 1

# ---- Benchmark ----

bench:
	@bash scripts/benchmark.sh

# ---- Coverage ----

cover:
	$(GO) tool cover -html=$(COVER_FILE) -o $(COVER_HTML)
	@echo "Coverage report: $(COVER_HTML)"

cover-check:
	@coverage=$$($(GO) tool cover -func=$(COVER_FILE) | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total coverage: $${coverage}%"; \
	if [ "$$(echo "$${coverage} < $(COVER_THRESHOLD)" | bc)" -eq 1 ]; then \
		echo "Coverage $${coverage}% below threshold $(COVER_THRESHOLD)%"; \
		exit 1; \
	fi

# ---- Maintenance ----

tidy:
	$(GO) mod tidy
	cd examples/gin && $(GO) mod tidy
	cd examples/gozero-http && $(GO) mod tidy 2>/dev/null || true
	cd examples/grpc && $(GO) mod tidy 2>/dev/null || true

fmt:
	$(GO) fmt ./...
	goimports -w . || $(GO) fmt ./...

clean:
	rm -rf bin/ $(COVER_FILE) $(COVER_HTML)
	$(GO) clean -cache -testcache
