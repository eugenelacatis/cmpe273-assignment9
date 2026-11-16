// Payment Service: POST /pay approves a payment. The artificial delay can be
// changed at runtime with POST /admin/delay?ms=N so the lab can inject and
// remove the failure without restarting.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/eugenelacatis/cmpe273-assignment9/internal/obs"
)

const service = "payment"

func main() {
	addr := env("PAYMENT_ADDR", ":8082")
	metrics := obs.NewMetrics(10)
	var delayMs atomic.Int64
	if v, err := strconv.Atoi(env("PAYMENT_DELAY_MS", "0")); err == nil {
		delayMs.Store(int64(v))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/pay", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(obs.HeaderRequestID)
		if id == "" {
			id = obs.NewID()
		}
		obs.Logf(service, id, "event=request_start method=%s path=%s delay_ms=%d", r.Method, r.URL.Path, delayMs.Load())

		if d := delayMs.Load(); d > 0 {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(obs.HeaderRequestID, id)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "approved", "request_id": id})

		lat := time.Since(start)
		metrics.Record(true, lat)
		obs.Logf(service, id, "event=request_end status=200 latency_ms=%.1f", obs.Ms(lat))
	})
	mux.HandleFunc("/admin/delay", func(w http.ResponseWriter, r *http.Request) {
		v, err := strconv.Atoi(r.URL.Query().Get("ms"))
		if err != nil || v < 0 {
			http.Error(w, "ms must be a non-negative integer", http.StatusBadRequest)
			return
		}
		delayMs.Store(int64(v))
		log.Printf("service=%s event=delay_changed delay_ms=%d", service, v)
		_ = json.NewEncoder(w).Encode(map[string]int{"delay_ms": v})
	})
	mux.HandleFunc("/metrics", metrics.Handler())

	log.Printf("service=%s event=listening addr=%s delay_ms=%d", service, addr, delayMs.Load())
	log.Fatal(http.ListenAndServe(addr, mux))
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
