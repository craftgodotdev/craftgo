package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// The errors the framework writes around the generated routes reach the client as JSON
// {"message": ...}: an unknown route, a wrong method, a failed validation, and a body over
// the cap, declared or read.
func TestFrameworkErrorsThroughGeneratedRoutes(t *testing.T) {
	ts := bootAll(t)
	capped := bootAll(t, func(srv *server.Server) { srv.SetDefaultMaxBodySize(16) })
	for _, tc := range []struct {
		name, method, url string
		body              io.Reader
		status            int
		want              string
	}{
		{"no route", "GET", ts.URL + "/api/nope", nil, 404, `{"message":"not found"}`},
		{"wrong method", "DELETE", ts.URL + "/api/bindings/page", nil, 405, `{"message":"method not allowed"}`},
		{"validation", "POST", ts.URL + "/api/numbers/multiple-of", strings.NewReader(`{"qty":7}`), 400, `{"message":"qty: must be a multiple of 5"}`},
		{"declared length over @maxBodySize", "POST", ts.URL + "/api/raw/rq/limits", strings.NewReader(strings.Repeat("x", 2048)), 413, `{"message":"request entity too large"}`},
		{"read past the default cap", "POST", capped.URL + "/api/numbers/multiple-of", io.NopCloser(strings.NewReader(`{"qty":5, "pad":"0123456789abcdef"}`)), 413, `{"message":"request entity too large"}`},
	} {
		req, err := http.NewRequest(tc.method, tc.url, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status || strings.TrimSpace(string(body)) != tc.want {
			t.Errorf("%s: got %d %q, want %d %s", tc.name, resp.StatusCode, body, tc.status, tc.want)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q, want JSON", tc.name, ct)
		}
	}
}
