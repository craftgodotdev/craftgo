package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RequireJSONBody takes a body of application/json or a +json type, any parameters and case
// aside; a body of any other media type, or of none, answers 415 as JSON.
func TestRequireJSONBody(t *testing.T) {
	for ct, want := range map[string]bool{
		"":                                  false,
		" ":                                 false,
		"application/json":                  true,
		"application/json; charset=utf-8":   true,
		"APPLICATION/JSON":                  true,
		"application/problem+json":          true,
		"application/vnd.api+json; v=2":     true,
		"text/plain":                        false,
		"application/x-www-form-urlencoded": false,
		"multipart/form-data; boundary=x":   false,
		"application/json-seq":              false,
		"text/json":                         false,
		"application/+json":                 false,
		"json":                              false,
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		if got := RequireJSONBody(rec, r); got != want {
			t.Errorf("%q: got %v, want %v", ct, got, want)
		}
		if !want && (rec.Code != http.StatusUnsupportedMediaType || rec.Body.String() != "{\"message\":\"unsupported media type\"}\n") {
			t.Errorf("%q: answered %d %q, want 415 as JSON", ct, rec.Code, rec.Body.String())
		}
	}
}

// A request without a Content-Type passes only when it has no body, for the decoder to report
// missing: a body of unknown length counts as one.
func TestRequireJSONBodyWithoutContentType(t *testing.T) {
	for name, c := range map[string]struct {
		body io.Reader
		want bool
	}{
		"no body":      {nil, true},
		"empty body":   {strings.NewReader(""), true},
		"body":         {strings.NewReader("{}"), false},
		"chunked body": {io.MultiReader(strings.NewReader("{}")), false},
	} {
		rec := httptest.NewRecorder()
		if got := RequireJSONBody(rec, httptest.NewRequest(http.MethodPost, "/", c.body)); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
		if !c.want && rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: answered %d, want 415", name, rec.Code)
		}
	}
}

// A form route's body that is no multipart/form-data is answered 415, not a failed validation.
func TestWriteValidationErrorNotMultipart(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	WriteValidationError(rec, r, r.ParseMultipartForm(1<<20))
	if rec.Code != http.StatusUnsupportedMediaType || rec.Body.String() != "{\"message\":\"unsupported media type\"}\n" {
		t.Errorf("got %d %q, want 415", rec.Code, rec.Body.String())
	}
}
