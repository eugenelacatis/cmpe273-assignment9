// Checkout Service: POST /checkout calls the Payment Service with a timeout.
// A timeout or error is counted and logged, and the service keeps running.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/eugenelacatis/cmpe273-assignment9/internal/obs"
)

const service = "checkout"

func main() {
	addr := env("CHECKOUT_ADDR", ":8081")
	paymentURL := env("PAYMENT_URL", "http://localhost:8082/pay")
	timeout := 2 * time.Second
	if d, err := time.ParseDuration(env("PAYMENT_TIMEOUT", "2s")); err == nil {
		timeout = d
	}
	metrics := obs.NewMetrics(10)
	client := &http.Client{}

	mux := http.NewServeMux()
	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(obs.HeaderRequestID)
		if id == "" {
			id = obs.NewID()
		}
		obs.Logf(service, id, "event=request_start method=%s path=%s", r.Method, r.URL.Path)

		status, body := process(client, paymentURL, id, timeout)

		lat := time.Since(start)
		ok := status == http.StatusOK
		metrics.Record(ok, lat)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(obs.HeaderRequestID, id)
		w.WriteHeader(status)
		body["request_id"] = id
		_ = json.NewEncoder(w).Encode(body)

		result := "success"
		if !ok {
			result = "failure"
		}
		obs.Logf(service, id, "event=request_end status=%d result=%s latency_ms=%.1f", status, result, obs.Ms(lat))
	})
	mux.HandleFunc("/metrics", metrics.Handler())

	log.Printf("service=%s event=listening addr=%s payment_url=%s payment_timeout=%s", service, addr, paymentURL, timeout)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// process calls payment and maps the outcome to an HTTP status and body.
func process(client *http.Client, url, id string, timeout time.Duration) (int, map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	req.Header.Set(obs.HeaderRequestID, id)

	callStart := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			obs.Logf(service, id, "event=payment_timeout timeout=%s waited_ms=%.1f", timeout, obs.Ms(time.Since(callStart)))
			return http.StatusGatewayTimeout, map[string]string{"status": "failed", "error": "payment timeout"}
		}
		obs.Logf(service, id, "event=payment_error err=%q", err.Error())
		return http.StatusBadGateway, map[string]string{"status": "failed", "error": "payment unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		obs.Logf(service, id, "event=payment_error payment_status=%d", resp.StatusCode)
		return http.StatusBadGateway, map[string]string{"status": "failed", "error": "payment rejected"}
	}
	obs.Logf(service, id, "event=payment_ok latency_ms=%.1f", obs.Ms(time.Since(callStart)))
	return http.StatusOK, map[string]string{"status": "order placed"}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
