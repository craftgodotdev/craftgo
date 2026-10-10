package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// resetCookieOptions restores the default cookie attributes when the test ends.
func resetCookieOptions(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = SetCookieOptions(DefaultCookieOptions()) })
}

// A cookie a handler sets takes path /, HttpOnly, Secure and SameSite=Lax by default.
func TestSetCookieDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCookie(rec, "sid", "abc")
	if got, want := rec.Header().Get("Set-Cookie"), "sid=abc; Path=/; HttpOnly; Secure; SameSite=Lax"; got != want {
		t.Errorf("Set-Cookie = %q, want %q", got, want)
	}
}

// Installed options replace each attribute; an empty path and SameSite send none.
func TestSetCookieOptions(t *testing.T) {
	resetCookieOptions(t)
	if err := SetCookieOptions(CookieOptions{Domain: "example.com", Secure: true, SameSite: "None"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SetCookie(rec, "sid", "abc")
	if got, want := rec.Header().Get("Set-Cookie"), "sid=abc; Domain=example.com; Secure; SameSite=None"; got != want {
		t.Errorf("Set-Cookie = %q, want %q", got, want)
	}
	if err := SetCookieOptions(CookieOptions{}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	SetCookie(rec, "sid", "abc")
	if got := rec.Header().Get("Set-Cookie"); got != "sid=abc" {
		t.Errorf("Set-Cookie = %q, want no attribute", got)
	}
}

// An unknown SameSite, or SameSite=None without Secure, is refused and changes nothing.
func TestSetCookieOptionsRefuses(t *testing.T) {
	resetCookieOptions(t)
	for _, o := range []CookieOptions{{SameSite: "loose", Secure: true}, {SameSite: "none"}} {
		if err := SetCookieOptions(o); err == nil {
			t.Errorf("%+v: want an error", o)
		}
	}
	rec := httptest.NewRecorder()
	SetCookie(rec, "sid", "abc")
	if got := rec.Header().Get("Set-Cookie"); got != "sid=abc; Path=/; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("a refused option changed the cookie: %q", got)
	}
}

// cookieErr is a StatusError whose fields set a cookie, as a generated error with a @cookie field.
type cookieErr struct{}

func (cookieErr) Error() string   { return "conflict" }
func (cookieErr) HTTPStatus() int { return http.StatusConflict }
func (cookieErr) ResponseCookies() []*http.Cookie {
	return []*http.Cookie{{Name: "retry", Value: "9"}}
}

// WriteError sets an error's cookies with the installed attributes.
func TestWriteErrorSetsErrorCookies(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/a/b", nil), cookieErr{})
	if rec.Code != http.StatusConflict || rec.Header().Get("Set-Cookie") != "retry=9; Path=/; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("got %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}
