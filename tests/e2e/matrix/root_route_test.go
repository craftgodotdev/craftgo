package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The root route serves the path a client of the document calls, the base path with its trailing
// slash, and no path below it: an unknown path is the framework's 404, not the root's answer.
func TestRootRouteServesItsOnePath(t *testing.T) {
	ts := bootAll(t)
	get := func(path string) (int, string) {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(raw))
	}
	if code, body := get("/api/"); code != http.StatusOK || body != `{"items":[]}` {
		t.Errorf("/api/: got %d %s, want the root's 200", code, body)
	}
	// The client follows the mux's redirect to the trailing slash.
	if code, body := get("/api"); code != http.StatusOK || body != `{"items":[]}` {
		t.Errorf("/api: got %d %s, want the root's 200 after the redirect", code, body)
	}
	for _, path := range []string{"/api/.env", "/api/no/such/route"} {
		if code, body := get(path); code != http.StatusNotFound || body != `{"message":"not found"}` {
			t.Errorf("%s: got %d %s, want 404", path, code, body)
		}
	}
}
