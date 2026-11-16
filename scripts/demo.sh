#!/usr/bin/env bash
# Runs both services, then: 1) success, 2) payment delay -> timeout, 3) recovery.
# Service logs go to evidence/*.log; this script's terminal output is what
# evidence/demo-output.txt captures.
set -u
cd "$(dirname "$0")/.."

CHECKOUT=http://localhost:8081
PAYMENT=http://localhost:8082
mkdir -p evidence bin
go build -o bin/payment ./cmd/payment && go build -o bin/checkout ./cmd/checkout || exit 1

: > evidence/payment.log
: > evidence/checkout.log
PAYMENT_TIMEOUT=2s ./bin/payment  >> evidence/payment.log  2>&1 &
PAY_PID=$!
PAYMENT_TIMEOUT=2s ./bin/checkout >> evidence/checkout.log 2>&1 &
CHK_PID=$!
trap 'kill $PAY_PID $CHK_PID 2>/dev/null' EXIT
sleep 1

order() { # prints "<http status> <seconds> <body>"
  curl -s -o /tmp/lab9_body -w "HTTP %{http_code}  total=%{time_total}s  " -X POST "$CHECKOUT/checkout"
  cat /tmp/lab9_body
}
metrics() {
  curl -s "$CHECKOUT/metrics" | tr -d '\n ' ; echo
}

echo "=== Phase 1: normal request ==="
for i in 1 2 3; do order; done
echo "checkout metrics:"; metrics

echo
echo "=== Phase 2: inject 3000ms payment delay (checkout timeout is 2s) ==="
curl -s -X POST "$PAYMENT/admin/delay?ms=3000"; echo
for i in 1 2 3; do order; done
echo "checkout metrics:"; metrics
echo "checkout still alive:"; curl -s -o /dev/null -w "GET /metrics -> HTTP %{http_code}\n" "$CHECKOUT/metrics"

echo
echo "=== Phase 3: remove delay, recover ==="
curl -s -X POST "$PAYMENT/admin/delay?ms=0"; echo
for i in $(seq 1 10); do order; done
echo "checkout metrics:"; metrics

# Let the last delayed payment request finish so its log line is complete.
sleep 3.5

echo
echo "=== Trace one failed request across both services ==="
ID=$(grep -m1 'event=payment_timeout' evidence/checkout.log | sed 's/.*request_id=\([0-9a-f]*\) .*/\1/')
echo "request_id=$ID"
grep -h "request_id=$ID" evidence/checkout.log evidence/payment.log | sort
