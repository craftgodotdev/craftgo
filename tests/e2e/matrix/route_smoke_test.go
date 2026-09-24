package matrix

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/craftgodotdev/craftgo/pkg/server"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/middleware"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// bootAll serves every service through the generated routes.RegisterAll,
// with every declared middleware assigned.
func bootAll(t *testing.T) *httptest.Server {
	t.Helper()
	svc := svccontext.NewServiceContext()
	svc.Audit = middleware.NewAuditMiddleware()
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware()
	svc.BasicAuth = middleware.NewBasicAuthMiddleware()
	svc.ProfileAuth = middleware.NewProfileAuthMiddleware(authToken)
	svc.RateLimit = middleware.NewRateLimitMiddleware()
	svc.RequestStamp = middleware.NewRequestStampMiddleware()
	svc.Timing = middleware.NewTimingMiddleware()
	srv := server.New(svc, server.WithoutDefaultHealth())
	routes.RegisterAll(srv, svc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// specOperations returns every (method, path) in docs/openapi.yaml, sorted
// by path, then method.
func specOperations(t *testing.T) [][2]string {
	t.Helper()
	raw, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	verbs := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}
	var ops [][2]string
	for p, item := range doc.Paths {
		for v := range item {
			if verbs[v] {
				ops = append(ops, [2]string{strings.ToUpper(v), p})
			}
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i][1] != ops[j][1] {
			return ops[i][1] < ops[j][1]
		}
		return ops[i][0] < ops[j][0]
	})
	return ops
}

// smokeURL turns a spec path into a request path: the /api basePath first,
// every {param} filled with "x".
func smokeURL(specPath string) string {
	p := specPath
	for {
		i := strings.IndexByte(p, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			break
		}
		p = p[:i] + "x" + p[i+j+1:]
	}
	full := "/api" + p
	if len(full) > 1 {
		full = strings.TrimRight(full, "/")
	}
	return full
}

// Every operation in docs/openapi.yaml is mounted under its own verb.
func TestEveryRouteRegisteredAndHandled(t *testing.T) {
	ts := bootAll(t)
	ops := specOperations(t)
	if len(ops) < 100 {
		t.Fatalf("suspiciously few operations parsed from the spec: %d", len(ops))
	}
	client := ts.Client()
	var miss []string
	for _, op := range ops {
		verb, specPath := op[0], op[1]
		req, err := http.NewRequest(verb, ts.URL+smokeURL(specPath), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", verb, specPath, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		// Only the mux's own 404 text means unmounted; a typed NotFound
		// means the handler ran.
		muxMiss := resp.StatusCode == http.StatusNotFound && strings.TrimSpace(string(body)) == "404 page not found"
		if muxMiss || resp.StatusCode == http.StatusMethodNotAllowed {
			miss = append(miss, verb+" "+specPath+" → "+resp.Status)
		}
	}
	if len(miss) > 0 {
		t.Errorf("%d/%d spec operations not mounted:\n  %s", len(miss), len(ops), strings.Join(miss, "\n  "))
	}
	t.Logf("route smoke: %d operations live", len(ops))
}
