#!/usr/bin/env bash
# External cross-check of the confirmation step: ghz (gRPC) + autocannon (REST).
#
# CAVEAT: ghz and autocannon are different tools (Go vs Node, different latency
# resolution and load models), so their absolute numbers are NOT directly
# comparable as a REST-vs-gRPC verdict. For the fair comparison use the
# in-process harness instead: `make bench-confirmation`. This script is kept to
# demonstrate buf-generated gRPC working under a standard gRPC load tool.
#
# Requires: ghz (gRPC), autocannon (REST, via npx). Run from the repo root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROTO="notifier/proto/confirmation/v1/confirmation.proto"
PAYLOAD='{"email":"user@example.com","repo":"golang/go","confirm_url":"https://example.com/confirm?t=abc"}'
REQUESTS="${REQUESTS:-30000}"
CONCURRENCY="${CONCURRENCY:-50}"
DURATION="${DURATION:-10}"

cd "$ROOT"

echo "==> starting confirmbench (REST :8082, gRPC :9092)"
( cd notifier && go run ./cmd/confirmbench ) &
BENCH_PID=$!
trap 'kill "$BENCH_PID" 2>/dev/null || true' EXIT
sleep 3

echo
echo "==> gRPC  (ghz, n=$REQUESTS, c=$CONCURRENCY)"
ghz --insecure \
  --proto "$PROTO" \
  --call confirmation.v1.ConfirmationService.SendConfirmation \
  -d "$PAYLOAD" \
  -n "$REQUESTS" -c "$CONCURRENCY" \
  localhost:9092 | sed -n '1,30p'

echo
echo "==> REST  (autocannon, c=$CONCURRENCY, d=${DURATION}s)"
npx --yes autocannon \
  -c "$CONCURRENCY" -d "$DURATION" \
  -m POST -H 'content-type=application/json' \
  -b "$PAYLOAD" \
  http://localhost:8082/internal/confirmations
