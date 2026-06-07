package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeDocs(t *testing.T) {
	spec := []byte("openapi: 3.1.0\ninfo:\n  title: X\n")
	cases := []struct {
		ui       string
		wantPage string // a marker unique to that UI's CDN host
	}{
		{"redoc", "redoc.standalone.js"},
		{"swagger", "swagger-ui-bundle.js"},
		{"scalar", "@scalar/api-reference"},
		{"", "redoc.standalone.js"},      // default = redoc
		{"bogus", "redoc.standalone.js"}, // unknown falls back to redoc
	}
	for _, c := range cases {
		s := New(nil).ServeDocs(DocsOptions{Spec: spec, UI: c.ui})
		h := s.Handler()

		// spec route
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("ui=%q spec status %d", c.ui, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "openapi: 3.1.0") {
			t.Errorf("ui=%q spec body wrong: %q", c.ui, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
			t.Errorf("ui=%q spec content-type %q, want yaml", c.ui, ct)
		}

		// docs page route
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("ui=%q docs status %d", c.ui, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, c.wantPage) {
			t.Errorf("ui=%q docs page missing %q:\n%s", c.ui, c.wantPage, body)
		}
		if !strings.Contains(body, "/openapi.yaml") {
			t.Errorf("ui=%q docs page does not point at the spec path", c.ui)
		}
	}
}

func TestServeDocs_CustomPathsAndJSONSpec(t *testing.T) {
	s := New(nil).ServeDocs(DocsOptions{
		Spec:     []byte(`{"openapi":"3.1.0"}`),
		UI:       "redoc",
		Path:     "/reference",
		SpecPath: "/spec.json",
		Title:    "My API",
	})
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/spec.json", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
		t.Errorf("json spec: status %d ct %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/reference", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("custom docs path status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/spec.json") {
		t.Error("docs page should point at the custom spec path")
	}
	if !strings.Contains(body, "<title>My API</title>") {
		t.Error("docs page should use the custom title")
	}
	// default /docs must NOT be registered when a custom path is used
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("default /docs should be 404 when custom path set, got %d", rec.Code)
	}
}

func TestServeDocs_EmptySpecIsNoop(t *testing.T) {
	s := New(nil).ServeDocs(DocsOptions{Spec: nil})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("empty spec must register nothing, got %d", rec.Code)
	}
}
