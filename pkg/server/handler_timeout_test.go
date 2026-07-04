package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A per-method @timeout overrides the default handler timeout - even a LONGER
// one. Under a default of 10ms, a route whose own timeout is 1h sees a ~1h
// context deadline, so the default did not clamp it. Deadlines are inspected on
// the request context, so the test never actually waits.
func TestPerMethodTimeoutOverridesDefault(t *testing.T) {
	s := New(nil)
	s.SetDefaultHandlerTimeout(10 * time.Millisecond)
	var remaining time.Duration
	var hadDeadline bool
	route := WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dl, ok := r.Context().Deadline()
		hadDeadline = ok
		if ok {
			remaining = time.Until(dl)
		}
		w.WriteHeader(http.StatusOK)
	}), Limits{Timeout: time.Hour})
	s.Handle("GET /slow", route)
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
	if !hadDeadline {
		t.Fatal("expected a context deadline from @timeout")
	}
	if remaining < time.Minute {
		t.Errorf("per-method @timeout(1h) must override default(10ms); got ~%s remaining", remaining)
	}
}

// A route WITHOUT its own @timeout inherits the default handler timeout.
func TestDefaultHandlerTimeoutAppliesToUntimedRoute(t *testing.T) {
	s := New(nil)
	s.SetDefaultHandlerTimeout(30 * time.Second)
	var remaining time.Duration
	var hadDeadline bool
	s.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {
		dl, ok := r.Context().Deadline()
		hadDeadline = ok
		if ok {
			remaining = time.Until(dl)
		}
		w.WriteHeader(http.StatusOK)
	})
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !hadDeadline {
		t.Fatal("route without @timeout should inherit the default deadline")
	}
	if remaining < 20*time.Second || remaining > 30*time.Second {
		t.Errorf("expected ~30s default deadline, got %s", remaining)
	}
}

// No default and no @timeout imposes no deadline (preserving unbounded handlers).
func TestNoDefaultHandlerTimeoutMeansNoDeadline(t *testing.T) {
	s := New(nil)
	var hadDeadline bool
	s.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if hadDeadline {
		t.Error("no default and no @timeout should impose no deadline")
	}
}
