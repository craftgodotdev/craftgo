package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// cookieErr is a StatusError whose fields set a cookie, as a generated error with a @cookie field.
type cookieErr struct{}

func (cookieErr) Error() string   { return "conflict" }
func (cookieErr) HTTPStatus() int { return http.StatusConflict }
func (cookieErr) ResponseCookies() []*http.Cookie {
	return []*http.Cookie{{Name: "retry", Value: "9", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}}
}

// WriteError sets an error's cookies as the error returns them.
func TestWriteErrorSetsErrorCookies(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/a/b", nil), cookieErr{})
	if rec.Code != http.StatusConflict || rec.Header().Get("Set-Cookie") != "retry=9; Path=/; HttpOnly; SameSite=Strict" {
		t.Errorf("got %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}
