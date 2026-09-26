package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A WithLimits timeout replaces the default handler timeout, even when longer.
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

// A WithLimits timeout replaces the default one on a route the default body cap wraps.
func TestOwnTimeoutSurvivesTheDefaultBodyCap(t *testing.T) {
	s := New(nil)
	s.SetDefaultHandlerTimeout(10 * time.Millisecond)
	s.SetDefaultMaxBodySize(8)
	var remaining time.Duration
	route := WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dl, ok := r.Context().Deadline(); ok {
			remaining = time.Until(dl)
		}
		w.WriteHeader(http.StatusOK)
	}), Limits{Timeout: time.Hour})
	s.Handle("POST /slow", route)
	h := s.Handler()

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/slow", strings.NewReader("small")))
	if remaining < time.Minute {
		t.Errorf("own timeout(1h) must replace the default(10ms); got ~%s remaining", remaining)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/slow", strings.NewReader("more than eight bytes")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("the default body cap must still apply: status %d, want 413", rec.Code)
	}
}

// A route without its own timeout gets the default handler timeout.
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

// A route has no deadline without a default or its own timeout.
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
