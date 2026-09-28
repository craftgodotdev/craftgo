package matrix

import (
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A @hidden method, extend block or service is still served.
func TestHiddenMethodsServed(t *testing.T) {
	ts := bootAll(t)
	for _, c := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"GET", "/api/hidden/note", "", http.StatusOK, `{"text":"read"}`},
		{"GET", "/api/hidden/peek?tier=Low", "", http.StatusOK, `{"items":[{"key":"Low"}]}`},
		{"GET", "/api/hidden/peek?tier=High", "", http.StatusForbidden, `{"code":"SEALED","message":"Forbidden"}`},
		{"POST", "/api/hidden/rotate", `{"key":"k1"}`, http.StatusCreated, `{"text":"rotated k1"}`},
		{"GET", "/api/hidden-ops/ping", "", http.StatusOK, `{"text":"pong"}`},
	} {
		req, err := http.NewRequest(c.method, ts.URL+c.path, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.code || strings.TrimSpace(string(raw)) != c.want {
			t.Errorf("%s %s = %d %s, want %d %s", c.method, c.path, resp.StatusCode, raw, c.code, c.want)
		}
	}
}

// The document leaves out each hidden method and each schema and security
// scheme only hidden methods use, and keeps what a documented method uses.
func TestHiddenMethodsUndocumented(t *testing.T) {
	raw, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]any `yaml:"paths"`
		Components struct {
			Schemas         map[string]any `yaml:"schemas"`
			SecuritySchemes map[string]any `yaml:"securitySchemes"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	check := func(what string, got map[string]any, kept, gone []string) {
		t.Helper()
		for _, name := range kept {
			if _, ok := got[name]; !ok {
				t.Errorf("%s %s missing; have %v", what, name, slices.Sorted(maps.Keys(got)))
			}
		}
		for _, name := range gone {
			if _, ok := got[name]; ok {
				t.Errorf("%s %s documented, want it hidden", what, name)
			}
		}
	}
	check("path", doc.Paths, []string{"/hidden/note"}, []string{"/hidden/peek", "/hidden/rotate", "/hidden-ops/ping"})
	check("schema", doc.Components.Schemas,
		[]string{"Note", "ReadRespBody"},
		[]string{"Secret", "Stash", "Tier", "VaultOfSecret", "SealedErr", "PeekRespBody", "RotateReqBody", "RotateRespBody", "PingRespBody"})
	check("security scheme", doc.Components.SecuritySchemes, []string{"ProfileAuth"}, []string{"InternalKey"})
}
