package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

// livenessHandler responds 200 with `{"status":"ok"}` once the server is
// ready to accept traffic. Liveness is a simple "process alive" probe,
// distinct from readiness which runs the registered checks.
func (s *Server) livenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

// readinessHandler runs every registered check in parallel and returns
// 200 only when every probe succeeds. Probes that exceed their timeout
// are recorded as failed.
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
				} else {
					results[name] = "ok"
				}
				resMu.Unlock()
			}(name, hc)
		}
		wg.Wait()

		ok := true
		for _, v := range results {
			if v != "ok" {
				ok = false
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": map[bool]string{true: "ready", false: "not_ready"}[ok],
			"checks": results,
		})
	})
}
