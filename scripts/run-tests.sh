#!/bin/bash
# =============================================================================
# Test runner — unit, integration, and optional Redis integration tests.
#
# Usage:
#   ./scripts/run-tests.sh              # unit + integration (memory)
#   ./scripts/run-tests.sh --redis      # include Redis integration tests
#   ./scripts/run-tests.sh --coverage   # generate coverage report
#   ./scripts/run-tests.sh --all        # everything
# =============================================================================

set -euo pipefail

REDIS_TESTS=false
COVERAGE=false
RACE=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --redis)   REDIS_TESTS=true ;;
    --coverage) COVERAGE=true ;;
    --all)     REDIS_TESTS=true; COVERAGE=true ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
  shift
done

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

echo "=== Running tests ==="

# Unit + integration tests (memory backend)
if [ "$COVERAGE" = true ]; then
  echo "--- Unit & integration tests (with coverage) ---"
  go test ./... -count=1 -coverprofile=coverage.out -covermode=atomic -timeout=120s
  go tool cover -func=coverage.out | tail -1
  go tool cover -html=coverage.out -o coverage.html
  echo "Coverage report: coverage.html"
else
  echo "--- Unit & integration tests ---"
  go test ./... -count=1 -timeout=120s
fi

# Example app tests
echo ""
echo "--- Example app tests ---"
cd examples/gin && go test ./... -count=1 -timeout=60s
cd "$ROOT_DIR"

# Redis integration tests
if [ "$REDIS_TESTS" = true ]; then
  echo ""
  echo "--- Redis integration tests ---"
  REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"

  if ! redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR#*:}" ping > /dev/null 2>&1; then
    echo "Redis not reachable at $REDIS_ADDR. Skipping integration tests."
    echo "Start Redis: docker compose up -d redis"
    exit 1
  fi

  REDIS_ADDR="$REDIS_ADDR" go test -tags=integration -count=1 -v -timeout=120s ./tests/
fi

echo ""
echo "=== All tests passed ==="
