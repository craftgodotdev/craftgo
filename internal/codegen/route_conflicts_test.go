package codegen

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func TestPatternsConflict(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// The ecommerce case: cross-over (literal/wildcard swapped) → conflict.
		{"/orders/{id}/track", "/orders/by-status/{status}", true},
		// One strictly more specific (literal beats wildcard, same elsewhere) → OK.
		{"/orders/{id}", "/orders/by-status", false},
		{"/orders/health", "/orders/{id}", false},
		// Distinct trailing literals on the same {id} prefix → disjoint → OK.
		{"/orders/{id}/cancel", "/orders/{id}/ship", false},
		// Different segment counts can't overlap (single-segment wildcards).
		{"/orders/{id}", "/orders/{id}/track", false},
		// Identical pattern (wildcard name is irrelevant to matching) → conflict.
		{"/orders/{id}", "/orders/{oid}", true},
		// Same literal path → conflict (exact duplicate).
		{"/orders/list", "/orders/list", true},
		// Both wildcard at the cross position but a distinct literal elsewhere.
		{"/a/{x}/b", "/a/{y}/c", false},
		// Two cross-overs deeper in the path → conflict.
		{"/a/{x}/c", "/a/b/{y}", true},
	}
	for _, c := range cases {
		if got := patternsConflict(c.a, c.b); got != c.want {
			t.Errorf("patternsConflict(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
		// Symmetric.
		if got := patternsConflict(c.b, c.a); got != c.want {
			t.Errorf("patternsConflict(%q,%q) [swapped] = %v, want %v", c.b, c.a, got, c.want)
		}
	}
}

func TestValidateRouteConflicts(t *testing.T) {
	cfg := sampleConfig()
	cfg.OpenAPI.BasePath = "/api"

	// Conflicting design: /{id}/track vs /by-status/{status} under one service.
	root, files := projectFiles(t, map[string]string{
		"m/x.craftgo": `package m
type IDReq { id string @path }
type StatusReq { status string @path }
type Resp { ok bool }
@prefix("/orders")
service OrderService {
    get Track /{id}/track { request IDReq  response Resp }
    get Filter /by-status/{status} { request StatusReq  response Resp }
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	msgs := ValidateRouteConflicts(proj, cfg)
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "Track") || !strings.Contains(msgs[0], "Filter") {
		t.Errorf("message should name both methods: %s", msgs[0])
	}

	// Clean design: the same two routes disambiguated (filter via a literal
	// sub-path) must produce no conflict.
	root2, files2 := projectFiles(t, map[string]string{
		"m/x.craftgo": `package m
type IDReq { id string @path }
type Resp { ok bool }
@prefix("/orders")
service OrderService {
    get Track /{id}/track { request IDReq  response Resp }
    get Filter /by-status { response Resp }
    get Get /{id} { request IDReq  response Resp }
}`,
	})
	proj2, diags2 := semantic.AnalyzeProject(files2, semantic.Options{DesignRoot: root2})
	if len(diags2) > 0 {
		t.Fatalf("semantic: %v", diags2)
	}
	if msgs := ValidateRouteConflicts(proj2, cfg); len(msgs) != 0 {
		t.Errorf("clean routes should not conflict, got: %v", msgs)
	}
}
