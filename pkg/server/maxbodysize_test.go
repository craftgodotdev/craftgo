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

// Without a default cap a large body is accepted.
func TestDefaultMaxBodySizeUnsetHasNoCap(t *testing.T) {
	var got string
	s := New(nil)
	s.Handle("POST /echo", readBody(t, &got))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("x", 1<<20))))
	if rec.Code != http.StatusOK || len(got) != 1<<20 {
		t.Errorf("no default cap: status %d, the handler read %d bytes; want 200 and all %d", rec.Code, len(got), 1<<20)
	}
}
