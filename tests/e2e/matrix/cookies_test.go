package matrix

import (
	"net/http"
	"slices"
	"testing"
)

// cookiesGet answers GET /api/cookies/a<path> with its status and Set-Cookie lines.
func cookiesGet(t *testing.T, url, path string) (int, []string) {
	t.Helper()
	resp, err := http.Get(url + "/api/cookies/a" + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Values("Set-Cookie")
}

// A cookie a response or an error sets takes the attributes its @cookie declares, path / instead
// of the request's directory, HttpOnly, Secure and SameSite=Lax for those it leaves out; a
// `cookie` field goes out as the logic set it, under its declared name.
func TestCookieAttributes(t *testing.T) {
	ts := bootAll(t)
	for _, c := range []struct {
		path string
		code int
		want []string
	}{
		{"/login", http.StatusOK, []string{"sid=t1; Path=/; HttpOnly; Secure; SameSite=Lax"}},
		{"/login?days=2", http.StatusOK, []string{
			"sid=t1; Path=/; HttpOnly; Secure; SameSite=Lax",
			"remember=r1; Path=/; Max-Age=172800; HttpOnly; Secure; SameSite=Strict",
		}},
		{"/logout", http.StatusOK, []string{"sid=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax"}},
		{"/lock", http.StatusConflict, []string{"lock=noon; Path=/api; Max-Age=3600; Secure; SameSite=Lax"}},
		// A success whose body fails to encode goes out as the 500, without its cookie.
		{"/broken", http.StatusInternalServerError, nil},
	} {
		if code, got := cookiesGet(t, ts.URL, c.path); code != c.code || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %d %q, want %d %q", c.path, code, got, c.code, c.want)
		}
	}
}
