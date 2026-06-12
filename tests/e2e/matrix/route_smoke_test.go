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

// bootAll stands up EVERY service in the matrix on one httptest server via the
// generated umbrella RegisterAll — the same wiring main.go performs — with all
// declared middlewares assigned. The per-feature harness (boot) registers only
// the services its roundtrip tests implement; this one exists so the route
// smoke below exercises the full registered surface.
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

// specOperations reads every (method, path) pair from the committed OpenAPI
// document — the spec is the contract, so walking it keeps the smoke in
// lock-step with the design without a hand-maintained route list.
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

// smokeURL maps a spec path to a request URL: the manifest basePath is
// prepended (spec paths omit it) and every {param} is filled with a literal —
// @path params are string-backed by the binding rules, so "x" always binds
// (validators may still 400, which the smoke counts as a live handler).
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

// TestEveryRouteRegisteredAndHandled walks every operation in the OpenAPI
// document against the fully-registered server. A 404 means the route never
// registered (the spec advertises an endpoint the server doesn't mount); a
// 405 means it registered under the wrong verb. Any other status — 2xx from
// a handler, 400/413 from the binder/validator rejecting the probe input —
// proves the route is live and its parse→validate chain runs. This is the
// net for registration-time regressions that compile fine and only fail at
// boot or first request.
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
		// An app-level 404 (a typed NotFound error for the probe's fake id)
		// proves the handler ran; only the mux's own "404 page not found"
		// text means the route never registered.
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
