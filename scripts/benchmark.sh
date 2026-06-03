#!/bin/bash
# =============================================================================
# Automated benchmark suite for the go-zero idempotency plugin.
#
# Prerequisites:
#   - go-wrk installed:  go install github.com/tsliwowicz/go-wrk@latest
#   - Example app running on localhost:8080
#
# Usage:
#   chmod +x scripts/benchmark.sh
#   ./scripts/benchmark.sh                    # run all scenarios
#   ./scripts/benchmark.sh --quick            # quick run (5s each)
#   ./scripts/benchmark.sh --output report.md # output to file
# =============================================================================

set -euo pipefail

# ---- Config ----
DURATION="${DURATION:-10s}"
CONNECTIONS="${CONNECTIONS:-50}"
HOST="${HOST:-http://localhost:8080}"
ENDPOINT="${ENDPOINT:-/api/orders}"
WRK="${WRK:-go-wrk}"
QUICK="${QUICK:-false}"
OUTPUT="${OUTPUT:-}"

# Parse flags
while [[ $# -gt 0 ]]; do
  case $1 in
    --quick) QUICK=true; DURATION="5s" ;;
    --output) OUTPUT="$2"; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
  shift
done

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[bench]${NC} $*"; }
warn() { echo -e "${YELLOW}[warn]${NC} $*"; }
err()  { echo -e "${RED}[error]${NC} $*"; }

# ---- Check preconditions ----
if ! command -v "$WRK" &> /dev/null; then
  err "go-wrk not found. Install with: go install github.com/tsliwowicz/go-wrk@latest"
  exit 1
fi

if ! curl -sf "$HOST/health" > /dev/null 2>&1; then
  err "Example app not reachable at $HOST/health. Start it first: cd examples/gin && go run ."
  exit 1
fi

# ---- Helpers ----
run_wrk() {
  local label="$1"; shift
  log "Running: $label"
  echo ""
  echo "=== $label ==="
  if [ "$QUICK" = true ]; then
    $WRK -c "$CONNECTIONS" -d "$DURATION" -M POST "$@" "$HOST$ENDPOINT" 2>&1 || true
  else
    $WRK -c "$CONNECTIONS" -d "$DURATION" -M POST "$@" "$HOST$ENDPOINT" 2>&1 || true
  fi
  echo ""
}

# ---- Run scenarios ----
START_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "# Idempotency Plugin Benchmark Report"
echo ""
echo "**Date:** $START_TIME"
echo "**Tool:** go-wrk"
echo "**Concurrency:** $CONNECTIONS connections"
echo "**Duration:** $DURATION per scenario"
echo "**Endpoint:** $HOST$ENDPOINT"
echo ""

# Scenario 1: Baseline — no idempotency key (pass-through)
run_wrk "1. Baseline (no key)" \
  -H "Content-Type: application/json" \
  -body '{"sku":"bench-test","qty":1}'

# Scenario 2: Acquire — new key (create record + execute handler + Complete)
KEY="bench-acquire-$(date +%s%N)"
run_wrk "2. Acquire (new key: $KEY)" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -body '{"sku":"bench-test","qty":1}'

# Scenario 3: Replay — same key, cached response
run_wrk "3. Replay (key: $KEY)" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -body '{"sku":"bench-test","qty":1}'

# Scenario 4: Conflict — same key, different body (fingerprint mismatch)
run_wrk "4. Conflict (key: $KEY, different body)" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -body '{"sku":"conflict","qty":99}'

END_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo ""
echo "**Completed:** $START_TIME → $END_TIME"
log "Benchmark complete."
