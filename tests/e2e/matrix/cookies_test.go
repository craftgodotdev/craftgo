package matrix

import (
	"net/http"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// cookiesGet answers GET /api/cookies/a<path> with its status and Set-Cookie line.
func cookiesGet(t *testing.T, url, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(url + "/api/cookies/a" + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Set-Cookie")
}

// A cookie a response or an error sets takes the server's cookie options: path / instead of the
// request's directory, HttpOnly, Secure and SameSite=Lax by default, the installed ones after.
func TestCookieAttributes(t *testing.T) {
	ts := bootAll(t)
	if code, c := cookiesGet(t, ts.URL, "/login"); code != http.StatusOK || c != "sid=t1; Path=/; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("response cookie: got %d %q", code, c)
	}
	if code, c := cookiesGet(t, ts.URL, "/lock"); code != http.StatusConflict || c != "lock=noon; Path=/; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("error cookie: got %d %q", code, c)
	}
	// A success whose body fails to encode goes out as the 500, without its cookie.
	if code, c := cookiesGet(t, ts.URL, "/broken"); code != http.StatusInternalServerError || c != "" {
		t.Errorf("encode failure: got %d %q, want 500 without the cookie", code, c)
	}
	t.Cleanup(func() { _ = server.SetCookieOptions(server.DefaultCookieOptions()) })
	if err := server.SetCookieOptions(server.CookieOptions{Path: "/api", SameSite: "strict"}); err != nil {
		t.Fatal(err)
	}
	if _, c := cookiesGet(t, ts.URL, "/login"); c != "sid=t1; Path=/api; SameSite=Strict" {
		t.Errorf("installed options: got %q", c)
	}
}
