package matrix

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A route's @maxBodySize and @timeout hold its own @middlewares: Signature, reading the whole body,
// meets the 64-byte cap with 413 and runs under the route's deadline.
func TestRouteLimitsHoldItsMiddlewares(t *testing.T) {
	ts := bootAll(t)
	post := func(body string) (int, string, string) {
		// Without a length, only the read meets the cap.
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/limits/hooks", io.NopCloser(bytes.NewReader([]byte(body))))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("X-Deadline"), strings.TrimSpace(string(raw))
	}
	if code, deadline, body := post(`{"event":"` + strings.Repeat("x", 1024) + `"}`); code != http.StatusRequestEntityTooLarge || body != `{"message":"request entity too large"}` {
		t.Errorf("a body past the cap: got %d %s, want 413", code, body)
	} else if deadline != "true" {
		t.Errorf("the middleware ran without the route's deadline")
	}
	if code, _, body := post(`{"event":"push"}`); code != http.StatusNoContent {
		t.Errorf("a body under the cap: got %d %s, want 204", code, body)
	}
}
