// Package metrics exposes a runtime-toggleable HTTP metrics middleware.
// When enabled, it records per-route request counts grouped by status
// class (1xx/2xx/3xx/4xx/5xx) and aggregate cumulative duration.
// Snapshots are cheap and lock-free; full Prometheus integration is
// out of scope for v1 — projects that need richer metrics should
// stack [otel.HTTPMiddleware] on top.
package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dropship-dev/craftgo/pkg/server"
)

// enabled is the runtime gate. atomic.Bool so toggling is safe from
// any goroutine.
var enabled atomic.Bool

// state holds the in-memory counters. Stored at package scope so the
// snapshot API can read it from anywhere; a mutex protects the maps
// because reset/snapshot/record may all race.
var (
	stateMu sync.Mutex
	counts  = map[Key]int64{}
	totalNs = map[Key]int64{}
)

// Key uniquely identifies a metric series — one row per
// method+path+status-class combination.
type Key struct {
	Method     string
	Path       string
	StatusKlas string // "2xx", "4xx", ...
}

// Snapshot is the read-only view returned to callers that want to
// expose metrics or verify them in tests.
type Snapshot struct {
	Counts    map[Key]int64
	TotalNs   map[Key]int64
}

// Init turns metrics ON. Subsequent calls to HTTPMiddleware return a
// recording wrapper. Idempotent.
func Init() { enabled.Store(true) }

// Disable returns the middleware to no-op mode without dropping the
// counters already accumulated.
func Disable() { enabled.Store(false) }

// IsEnabled reports the current toggle state.
func IsEnabled() bool { return enabled.Load() }

// Reset clears every counter. Useful for tests.
func Reset() {
	stateMu.Lock()
	defer stateMu.Unlock()
	counts = map[Key]int64{}
	totalNs = map[Key]int64{}
}

// SnapshotCounters returns a copy of the current state. The returned
// maps are independent of the package storage so callers can iterate
// without holding any lock.
func SnapshotCounters() Snapshot {
	stateMu.Lock()
	defer stateMu.Unlock()
	c := make(map[Key]int64, len(counts))
	for k, v := range counts {
		c[k] = v
	}
	d := make(map[Key]int64, len(totalNs))
	for k, v := range totalNs {
		d[k] = v
	}
	return Snapshot{Counts: c, TotalNs: d}
}

// HTTPMiddleware returns a server.Middleware that records one row per
// request. When the gate is off the wrapper is a pass-through.
func HTTPMiddleware() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled.Load() {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			record(r.Method, r.URL.Path, rec.status, time.Since(start))
		})
	}
}

// record updates the per-key counters under the package mutex.
func record(method, path string, status int, dur time.Duration) {
	k := Key{Method: method, Path: path, StatusKlas: classify(status)}
	stateMu.Lock()
	counts[k]++
	totalNs[k] += dur.Nanoseconds()
	stateMu.Unlock()
}

// classify reduces a status code to its class string (e.g. 207 → "2xx",
// 503 → "5xx") so the cardinality of the metric set stays bounded.
func classify(status int) string {
	switch status / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	}
	return "other"
}

// statusRecorder wraps http.ResponseWriter to capture the status code
// the downstream handler eventually wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before delegating.
func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

// Flush forwards to the underlying writer's Flusher so SSE / NDJSON
// handlers downstream keep their streaming capability.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
