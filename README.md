# CMPE 273 Lab 9: Logs, Metrics, and Request Tracing

Two small Go services. The Checkout Service calls the Payment Service for each order. Both write request logs and share a request ID, and Checkout keeps latency and success/failure metrics. I add a delay to Payment that is longer than Checkout's timeout, follow the failed request through both logs by its ID, then remove the delay and watch the metrics recover.

Standard library only. No dependencies.

## Running it

Requires Go 1.21 or newer.

```bash
git clone https://github.com/eugenelacatis/cmpe273-assignment9.git
cd cmpe273-assignment9
./scripts/demo.sh
```

`demo.sh` builds and starts both services, runs all three scenarios, and prints the results. Service logs land in `evidence/payment.log` and `evidence/checkout.log`. The terminal output is saved in `evidence/demo-output.txt`.

To run the services yourself in two terminals:

```bash
go run ./cmd/payment                 # :8082
go run ./cmd/checkout                # :8081

curl -X POST localhost:8081/checkout # place an order
curl localhost:8081/metrics          # checkout metrics
curl -X POST 'localhost:8082/admin/delay?ms=3000'  # inject delay
curl -X POST 'localhost:8082/admin/delay?ms=0'     # remove delay
```

Settings (environment variables): `CHECKOUT_ADDR`, `PAYMENT_ADDR`, `PAYMENT_URL`, `PAYMENT_TIMEOUT` (default `2s`), `PAYMENT_DELAY_MS` (default `0`).

## Architecture

```
                POST /checkout                       POST /pay
   Client ───────────────────▶ ┌──────────────┐ ───────────────────▶ ┌─────────────┐
                               │   Checkout   │   X-Request-ID: abc  │   Payment   │
          ◀─────────────────── │    :8081     │ ◀─────────────────── │    :8082    │
        200 / 504 + request_id │ 2s timeout   │                      │ delay knob  │
                               └──────┬───────┘                      └──────┬──────┘
                                      │ logs + /metrics                     │ logs + /metrics
                                      ▼                                     ▼
                              evidence/checkout.log                evidence/payment.log
```

| Path | Contents |
| --- | --- |
| `cmd/checkout/main.go` | `POST /checkout`, calls Payment with a timeout, `GET /metrics` |
| `cmd/payment/main.go` | `POST /pay`, `POST /admin/delay?ms=N` to set the delay at runtime, `GET /metrics` |
| `internal/obs/obs.go` | Request ID generation, log helper, metrics counters |
| `scripts/demo.sh` | Runs the three scenarios end to end |
| `evidence/` | `demo-output.txt`, `checkout.log`, `payment.log` from one run |

Checkout takes the `X-Request-ID` header from the incoming request or makes a new 8-byte hex ID, and sends the same header to Payment. Every log line from both services carries `request_id=...`. Checkout calls Payment through a context with a 2 second timeout. A timeout returns 504 to the client, is logged and counted as a failure, and the service keeps serving. Payment's delay is an atomic value, so I can change it with one `curl` and no restart.

Checkout's `/metrics` returns total, success and failure counts, error rate, and average latency, both for all requests and for the last 10. The last-10 numbers are what show recovery, since lifetime averages stay dirty for a long time after a failure.

## Results

Full output is in [`evidence/demo-output.txt`](evidence/demo-output.txt).

### 1. Successful request

Three orders with Payment healthy. All return 200 in a few milliseconds and the error rate is 0.

```
=== Phase 1: normal request ===
HTTP 200  total=0.009669s  {"request_id":"58c731d128d8e810","status":"order placed"}
HTTP 200  total=0.001014s  {"request_id":"bbcf82a0ea58b7ba","status":"order placed"}
HTTP 200  total=0.000831s  {"request_id":"b9f92d8d62e0938b","status":"order placed"}
checkout metrics:
{"requests_total":3,"requests_success":3,"requests_failure":0,"error_rate":0,"avg_latency_ms":2.29, ...}
```

The first request, `58c731d128d8e810`, in both logs:

```
service=checkout request_id=58c731d128d8e810 event=request_start method=POST path=/checkout
service=payment  request_id=58c731d128d8e810 event=request_start method=POST path=/pay delay_ms=0
service=payment  request_id=58c731d128d8e810 event=request_end status=200 latency_ms=0.6
service=checkout request_id=58c731d128d8e810 event=payment_ok latency_ms=5.8
service=checkout request_id=58c731d128d8e810 event=request_end status=200 result=success latency_ms=6.1
```

### 2. Timeout failure

I set Payment's delay to 3000 ms. Checkout's timeout is 2000 ms. All three orders come back as 504 after about 2 seconds, and Checkout still answers `/metrics` afterward, so it did not crash.

```
=== Phase 2: inject 3000ms payment delay (checkout timeout is 2s) ===
{"delay_ms":3000}
HTTP 504  total=2.002639s  {"error":"payment timeout","request_id":"3c25a540657537a2","status":"failed"}
HTTP 504  total=2.002657s  {"error":"payment timeout","request_id":"f1d82afd73a93ebf","status":"failed"}
HTTP 504  total=2.002710s  {"error":"payment timeout","request_id":"e84617a1667d13f8","status":"failed"}
checkout metrics:
{"requests_total":6,"requests_success":3,"requests_failure":3,"error_rate":0.5,"avg_latency_ms":1001.99, ...}
checkout still alive:
GET /metrics -> HTTP 200
```

Grepping both logs for the first failed ID, `3c25a540657537a2`, shows the whole story in order:

```
14:42:11 service=checkout request_id=3c25a540657537a2 event=request_start method=POST path=/checkout
14:42:11 service=payment  request_id=3c25a540657537a2 event=request_start method=POST path=/pay delay_ms=3000
14:42:13 service=checkout request_id=3c25a540657537a2 event=payment_timeout timeout=2s waited_ms=2001.4
14:42:13 service=checkout request_id=3c25a540657537a2 event=request_end status=504 result=failure latency_ms=2001.7
14:42:14 service=payment  request_id=3c25a540657537a2 event=request_end status=200 latency_ms=3001.5
```

Checkout gave up at 14:42:13. Payment kept working and finished at 14:42:14 after 3001 ms. Payment's own log says 200, so without the shared ID you would see a healthy Payment Service and a failing Checkout and have no way to connect them.

### 3. Recovery

I removed the delay and sent 10 more orders. All 10 return 200 in about 1 ms.

```
=== Phase 3: remove delay, recover ===
{"delay_ms":0}
HTTP 200  total=0.001572s  {"request_id":"5aa9f232fef754ef","status":"order placed"}
... (10 requests, all HTTP 200, 0.8 to 1.6 ms)
checkout metrics:
{"requests_total":16,"requests_success":13,"requests_failure":3,"error_rate":0.1875,"avg_latency_ms":376.01,
 "window_size":10,"window_error_rate":0,"window_avg_latency_ms":0.42}
```

| | Phase 1 | Phase 2 | Phase 3 |
| --- | --- | --- | --- |
| Error rate, last 10 requests | 0% | 50% (6 requests so far) | 0% |
| Avg latency, last 10 requests | 2.3 ms | 1002 ms | 0.4 ms |
| Error rate, all requests | 0% | 50% | 18.75% and falling |

The lifetime error rate is still 18.75% after recovery because it includes the three failures. It drops with every successful request. The last-10 window is already back to 0% and 0.4 ms.

## Reflection

Each of the three tools answered a different question during the timeout. The metrics told me something was wrong: the error rate jumped to 50% and average latency went from 2 ms to about 1 second. The logs told me what: Checkout logged `payment_timeout` after exactly 2 seconds. The shared request ID told me where: grepping one ID showed Payment received the request, was slow, and finished a second after Checkout gave up. Payment on its own looked healthy, so I would not have found the cause from its logs alone.
