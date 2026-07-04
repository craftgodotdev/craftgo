package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SetDefaultMaxBodySize installs a global BodyLimit via Handler(), so an
// oversized request is rejected even for a handler that never reads the body.
func TestSetDefaultMaxBodySizeEnforced(t *testing.T) {
	s := New(nil)
	s.SetDefaultMaxBodySize(10)
	s.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // never reads r.Body
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("more than ten bytes here"))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("global body cap should reject oversized with 413, got %d", rec.Code)
	}
}

// A per-method @maxBodySize takes priority over the default - even a LARGER
// value. Under a default of 10, a route whose own cap is 1000 accepts a 20-byte
// body: the default must not clamp a route that declares its own limit.
func TestPerMethodMaxBodySizeOverridesDefault(t *testing.T) {
	s := New(nil)
	s.SetDefaultMaxBodySize(10)
	// Emulates a generated route: the handler carries its own @maxBodySize.
	route := WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}), Limits{MaxBodySize: 1000})
	s.Handle("POST /up", route)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/up", strings.NewReader("twenty bytes of body!!"))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("per-method @maxBodySize(1000) must override default(10); 20-byte body should pass, got %d", rec.Code)
	}
}

// The default (0) installs no global cap, so a large body is accepted -
// preserving the pre-fix behaviour for callers that never set a cap.
func TestDefaultMaxBodySizeUnsetHasNoCap(t *testing.T) {
	s := New(nil)
	s.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("x", 1<<20)))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("no global cap by default: large body should pass, got %d", rec.Code)
	}
}
