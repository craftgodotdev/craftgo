package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// RequireJSONBody takes application/json and +json types, any parameters and case aside, and a
// body without a Content-Type; any other media type answers 415 as JSON.
func TestRequireJSONBody(t *testing.T) {
	for ct, want := range map[string]bool{
		"":                                  true,
		" ":                                 true,
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
