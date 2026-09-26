package server

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// DefaultLivenessPath and DefaultReadinessPath are the health probe routes unless
// [WithHealthPaths] sets others.
const (
	DefaultLivenessPath  = "/healthz"
	DefaultReadinessPath = "/readyz"
)

// HealthPaths is the override pair for [DefaultLivenessPath] and
// [DefaultReadinessPath].
type HealthPaths struct {
	Liveness  string
	Readiness string
}

// healthCheck pairs a probe function with its timeout.
type healthCheck struct {
	timeout time.Duration
	fn      func(context.Context) error
}

// run calls the check under its timeout. A panic fails the check with "panic: <value>" and
// is logged with its stack to [log.Default].
func (hc healthCheck) run(ctx context.Context, name string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, hc.timeout)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			log.Default().WithContext(ctx).Error("panic recovered in readiness check",
				log.String("check", name),
				log.Any("panic", rec),
				log.String("stack", string(debug.Stack())),
			)
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return hc.fn(ctx)
}

// WithHealthPaths overrides the default `/healthz` and `/readyz` routes.
func WithHealthPaths(p HealthPaths) Option {
	return func(s *Server) { s.healthPaths = p }
}

// WithoutDefaultHealth turns the health probes off.
func WithoutDefaultHealth() Option { return func(s *Server) { s.noHealth = true } }

// RegisterHealthCheck adds, or replaces, the readiness check name. Each readiness probe runs
// fn under a context with timeout, and a non-nil error or a panic answers 503.
func (s *Server) RegisterHealthCheck(name string, timeout time.Duration, fn func(context.Context) error) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthChecks[name] = healthCheck{timeout: timeout, fn: fn}
	return s
}

// probesLocked returns the probe handlers by path, or nil when health is off; the caller
// holds s.mu.
func (s *Server) probesLocked() map[string]http.Handler {
	if s.noHealth {
		return nil
	}
	guard := recovery(log.Default)
	return map[string]http.Handler{
		s.healthPaths.Liveness:  guard(s.livenessHandler()),
		s.healthPaths.Readiness: guard(s.readinessHandler()),
	}
}

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
		maps.Copy(checks, s.healthChecks)
		s.mu.Unlock()

		var wg sync.WaitGroup
		var resMu sync.Mutex
		results := map[string]string{}
		ok := true
		for name, hc := range checks {
			wg.Add(1)
			go func(name string, hc healthCheck) {
				defer wg.Done()
				err := hc.run(r.Context(), name)
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
