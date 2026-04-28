// Hand-filled implementation. Demo-grade rate limiter — production
// projects swap in a redis-backed implementation behind the same
// signature.

package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/dropship-dev/craftgo/pkg/server"
)

// NewRateLimitMiddleware returns a per-IP throttle. The window length
// and the max requests inside the window are configured at wire time
// so tests can squeeze the throttle aggressively while production
// keeps it loose.
func NewRateLimitMiddleware(maxPerWindow int, window time.Duration) server.Middleware {
	var mu sync.Mutex
	hits := map[string][]time.Time{}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			ip := r.RemoteAddr
			mu.Lock()
			cutoff := now.Add(-window)
			past := hits[ip]
			fresh := past[:0]
			for _, t := range past {
				if t.After(cutoff) {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) >= maxPerWindow {
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":        "RATE_LIMITED",
					"message":     "Too many requests",
					"retry_after": int(window.Seconds()),
				})
				return
			}
			hits[ip] = append(fresh, now)
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}
}
