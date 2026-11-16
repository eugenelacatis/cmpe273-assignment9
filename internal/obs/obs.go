// Package obs holds the small amount of observability code both services share:
// request IDs, structured request logs, and in-memory metrics.
package obs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

const HeaderRequestID = "X-Request-ID"

// NewID returns a random 8-byte hex request ID.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Metrics counts requests and keeps latency stats. Window* fields cover only
// the most recent windowSize requests so recovery is visible without a restart.
type Metrics struct {
	mu         sync.Mutex
	total      int
	success    int
	failure    int
	latencySum time.Duration
	recent     []sample
	windowSize int
}

type sample struct {
	ok      bool
	latency time.Duration
}

func NewMetrics(windowSize int) *Metrics {
	return &Metrics{windowSize: windowSize}
}

// Record adds one finished request.
func (m *Metrics) Record(ok bool, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.total++
	if ok {
		m.success++
	} else {
		m.failure++
	}
	m.latencySum += latency
	m.recent = append(m.recent, sample{ok, latency})
	if len(m.recent) > m.windowSize {
		m.recent = m.recent[1:]
	}
}

// Snapshot is the JSON shape served on /metrics.
type Snapshot struct {
	Total            int     `json:"requests_total"`
	Success          int     `json:"requests_success"`
	Failure          int     `json:"requests_failure"`
	ErrorRate        float64 `json:"error_rate"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	WindowSize       int     `json:"window_size"`
	WindowErrorRate  float64 `json:"window_error_rate"`
	WindowAvgLatency float64 `json:"window_avg_latency_ms"`
}

func (m *Metrics) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Total: m.total, Success: m.success, Failure: m.failure, WindowSize: len(m.recent)}
	if m.total > 0 {
		s.ErrorRate = float64(m.failure) / float64(m.total)
		s.AvgLatencyMs = ms(m.latencySum) / float64(m.total)
	}
	var wFail int
	var wSum time.Duration
	for _, r := range m.recent {
		if !r.ok {
			wFail++
		}
		wSum += r.latency
	}
	if n := len(m.recent); n > 0 {
		s.WindowErrorRate = float64(wFail) / float64(n)
		s.WindowAvgLatency = ms(wSum) / float64(n)
	}
	return s
}

// Handler serves the snapshot as JSON.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(m.Snapshot())
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Logf writes one log line tagged with the service and request ID.
func Logf(service, reqID, format string, args ...any) {
	log.Printf("service="+service+" request_id="+reqID+" "+format, args...)
}

// Ms formats a duration as fractional milliseconds for log lines.
func Ms(d time.Duration) float64 { return ms(d) }
