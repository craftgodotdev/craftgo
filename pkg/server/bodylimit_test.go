package server

import (
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

// A body within the cap reaches the handler whole.
func TestBodyLimitAllowsWithinCap(t *testing.T) {
	var got string
	rec := httptest.NewRecorder()
	BodyLimit(1024)(readBody(t, &got)).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("small body")))
	if rec.Code != http.StatusOK || got != "small body" {
		t.Errorf("within-cap body: status %d, the handler read %q; want 200 and the whole body", rec.Code, got)
	}
}

// Reading a capped body allocates nothing per Read.
func TestBodyLimitReadsAllocateNothing(t *testing.T) {
	var allocs float64
	h := BodyLimit(1 << 20)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		allocs = testing.AllocsPerRun(100, func() { _, _ = r.Body.Read(buf) })
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("x", 1<<16)))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if allocs != 0 {
		t.Errorf("a Read of a capped body allocated %.0f time(s), want 0", allocs)
	}
}
