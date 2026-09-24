package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BodyLimit answers 413 to an oversized Content-Length before the handler runs.
func TestBodyLimitRejectsOversizedContentLength(t *testing.T) {
	called := false
	h := BodyLimit(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true // never reads r.Body
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("clearly more than ten bytes"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body should be rejected with 413 before the handler, got %d", rec.Code)
	}
	if called {
		t.Error("handler must not run for an oversized declared body")
	}
}

// A body within the cap passes through to the handler unchanged.
func TestBodyLimitAllowsWithinCap(t *testing.T) {
	h := BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("small body"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("within-cap body should pass, got %d", rec.Code)
	}
}
