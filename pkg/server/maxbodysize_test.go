package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The default body cap rejects an oversized body even when the handler never reads it.
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

// A WithLimits body cap replaces the default cap, even when larger.
func TestPerMethodMaxBodySizeOverridesDefault(t *testing.T) {
	const body = "twenty bytes of body!!"
	var got string
	s := New(nil)
	s.SetDefaultMaxBodySize(10)
	s.Handle("POST /up", WithLimits(readBody(t, &got), Limits{MaxBodySize: 1000}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/up", strings.NewReader(body)))
	if rec.Code != http.StatusOK || got != body {
		t.Errorf("a 22-byte body under the route's cap of 1000 and the default of 10: status %d, the handler read %q; want 200 and the whole body", rec.Code, got)
	}
}

// A new Server caps a body at DefaultMaxBodySize, and a default cap of 0 lifts it.
func TestDefaultMaxBodySize(t *testing.T) {
	var got string
	capped := New(nil)
	capped.Handle("POST /echo", readBody(t, &got))
	under := strings.Repeat("x", DefaultMaxBodySize)
	rec := httptest.NewRecorder()
	capped.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(under)))
	if rec.Code != http.StatusOK || len(got) != DefaultMaxBodySize {
		t.Errorf("a body at the cap: status %d, read %d bytes; want 200 and all of it", rec.Code, len(got))
	}
	rec = httptest.NewRecorder()
	capped.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(under+"x")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body past the cap: status %d, want 413", rec.Code)
	}

	uncapped := New(nil)
	uncapped.SetDefaultMaxBodySize(0)
	uncapped.Handle("POST /echo", readBody(t, &got))
	rec = httptest.NewRecorder()
	uncapped.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(under+"x")))
	if rec.Code != http.StatusOK || len(got) != DefaultMaxBodySize+1 {
		t.Errorf("no cap: status %d, read %d bytes; want 200 and all of it", rec.Code, len(got))
	}
}
