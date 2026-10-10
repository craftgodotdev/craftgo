package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
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

// The docs page loads each UI asset at an exact version, with the integrity hash a browser checks
// it against.
func TestServeDocsPinsItsAssets(t *testing.T) {
	asset := regexp.MustCompile(`<(?:script|link)\b[^>]*\b(?:src|href)="https?://[^>]*>`)
	pinned := regexp.MustCompile(`(?:src|href)="https://cdn\.jsdelivr\.net/npm/(?:@[a-z-]+/)?[a-z-]+@\d+\.\d+\.\d+/[^"]+"`)
	hash := regexp.MustCompile(`\bintegrity="sha384-[A-Za-z0-9+/]{64}"`)
	for _, ui := range []DocsUI{DocsRedoc, DocsSwagger, DocsScalar} {
		tags := asset.FindAllString(docsHTML(ui, "/openapi.yaml", "API"), -1)
		if len(tags) == 0 {
			t.Errorf("%s: no asset found", ui)
		}
		for _, tag := range tags {
			if !pinned.MatchString(tag) || !hash.MatchString(tag) || !strings.Contains(tag, `crossorigin="anonymous"`) {
				t.Errorf("%s: %s is not pinned with its integrity hash", ui, tag)
			}
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
	// The default /docs is not registered.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("default /docs should be 404 when custom path set, got %d", rec.Code)
	}
}

// RegisterDocs refuses, registering neither route, a path that does not start with "/" or that
// a ServeMux refuses, two equal paths, and a path a health probe or a registered route answers.
func TestRegisterDocsRefusesAnUnusablePath(t *testing.T) {
	for name, c := range map[string]struct {
		opts DocsOptions
		want string
	}{
		"relative path":      {DocsOptions{Path: "docs"}, `docs Path "docs" does not start with "/"`},
		"relative spec path": {DocsOptions{SpecPath: "openapi.yaml"}, `docs SpecPath "openapi.yaml" does not start with "/"`},
		"unclean path":       {DocsOptions{Path: "/a/../docs"}, `docs Path "/a/../docs": `},
		"bad wildcard":       {DocsOptions{SpecPath: "/spec/{"}, `docs SpecPath "/spec/{": `},
		"equal paths":        {DocsOptions{Path: "/x", SpecPath: "/x"}, `docs Path and SpecPath are both "/x"`},
		"probe path":         {DocsOptions{Path: "/healthz"}, `docs Path "/healthz" is a health probe's path`},
		"route path":         {DocsOptions{Path: "/api/users"}, `docs Path "/api/users" is the path of the route "GET /api/users"`},
		"wildcard route":     {DocsOptions{SpecPath: "/api/users/{id}"}, `docs SpecPath "/api/users/{id}" is the path of the route "GET /api/users/{id}"`},
	} {
		s := New(nil)
		s.HandleFunc("GET /api/users", func(http.ResponseWriter, *http.Request) {})
		s.HandleFunc("GET /api/users/{id}", func(http.ResponseWriter, *http.Request) {})
		c.opts.Spec = []byte("openapi: 3.1.0\n")
		if err := s.RegisterDocs(c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", name, err, c.want)
		}
		for _, path := range []string{"/docs", "/openapi.yaml", "/x"} {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s answered %d, want nothing registered", name, path, rec.Code)
			}
		}
	}
}

// A probe path is free once the probes are off, and a route matching more than the docs path
// leaves it free.
func TestRegisterDocsTakesAPathNothingElseAnswers(t *testing.T) {
	s := New(nil, WithoutDefaultHealth())
	s.HandleFunc("GET /{path...}", func(http.ResponseWriter, *http.Request) {})
	if err := s.RegisterDocs(DocsOptions{Spec: []byte("openapi: 3.1.0\n"), Path: "/healthz"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<redoc") {
		t.Errorf("got %d %q, want the docs page", rec.Code, rec.Body.String())
	}
}

// ServeDocs panics with the error RegisterDocs returns.
func TestServeDocsPanicsWhereRegisterDocsFails(t *testing.T) {
	defer func() {
		if r := recover(); !strings.Contains(fmt.Sprint(r), `docs Path "docs" does not start with "/"`) {
			t.Errorf("recovered %v, want the path error", r)
		}
	}()
	New(nil).ServeDocs(DocsOptions{Spec: []byte("openapi: 3.1.0\n"), Path: "docs"})
}

func TestServeDocs_EmptySpecIsNoop(t *testing.T) {
	s := New(nil).ServeDocs(DocsOptions{Spec: nil})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("empty spec must register nothing, got %d", rec.Code)
	}
}
