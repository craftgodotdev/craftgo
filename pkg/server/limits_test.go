package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// bodyReadingMiddleware reads the whole body, as a webhook-signature check does, records what it
// read and whether the request carried a deadline, and answers 413 to a read past a cap.
func bodyReadingMiddleware(read *int, deadline *bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			*read = len(b)
			_, *deadline = r.Context().Deadline()
			if err != nil {
				WriteValidationError(w, r, err)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(b)))
			next.ServeHTTP(w, r)
		})
	}
}

// A route's own limits, and the server defaults standing in for those it leaves unset, hold its
// middlewares too: one reading the body meets the cap and sees the deadline.
func TestLimitsWrapTheRouteMiddlewares(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, c := range []struct {
		name  string
		setup func(*Server) http.Handler
	}{
		{"own limits", func(*Server) http.Handler {
			return WithLimits(ok, Limits{MaxBodySize: 64, Timeout: time.Minute})
		}},
		{"defaults", func(s *Server) http.Handler {
			s.SetDefaultMaxBodySize(64)
			s.SetDefaultHandlerTimeout(time.Minute)
			return ok
		}},
		{"own timeout, default cap", func(s *Server) http.Handler {
			s.SetDefaultMaxBodySize(64)
			return WithLimits(ok, Limits{Timeout: time.Minute})
		}},
	} {
		var read int
		var deadline bool
		s := New(nil, WithoutDefaultHealth())
		s.Handle("POST /hook", c.setup(s), bodyReadingMiddleware(&read, &deadline))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/hook", io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20))))
		req.ContentLength = -1
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge || read > 64 || !deadline {
			t.Errorf("%s: status %d, the middleware read %d bytes, deadline %v; want 413, at most 64, a deadline", c.name, rec.Code, read, deadline)
		}
	}
}

// A WithLimits handler served on its own, outside Handle, keeps its guards.
func TestWithLimitsServedAlone(t *testing.T) {
	var got string
	h := WithLimits(readBody(t, &got), Limits{MaxBodySize: 4})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("too long")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", rec.Code)
	}
}
