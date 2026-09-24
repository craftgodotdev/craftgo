package server

import (
	"context"
	"net/http"
	"sync"
)

// livenessHandler always answers 200 {"status":"ok"}.
func (s *Server) livenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypeJSON)
		_ = JSON().Encode(w, map[string]string{"status": "ok"})
	})
}

// readinessHandler runs the registered checks concurrently, waits for all of them, and
// answers 503 unless every one returns nil.
func (s *Server) readinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		checks := make(map[string]healthCheck, len(s.healthChecks))
		for k, v := range s.healthChecks {
			checks[k] = v
		}
		s.mu.Unlock()

		var wg sync.WaitGroup
		var resMu sync.Mutex
		results := map[string]string{}
		ok := true
		for name, hc := range checks {
			wg.Add(1)
			go func(name string, hc healthCheck) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(r.Context(), hc.timeout)
				defer cancel()
				err := hc.fn(ctx)
				resMu.Lock()
				if err != nil {
					results[name] = err.Error()
					ok = false
				} else {
					results[name] = "ok"
				}
				resMu.Unlock()
			}(name, hc)
		}
		wg.Wait()

		w.Header().Set("Content-Type", contentTypeJSON)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = JSON().Encode(w, map[string]any{
			"status": map[bool]string{true: "ready", false: "not_ready"}[ok],
			"checks": results,
		})
	})
}
