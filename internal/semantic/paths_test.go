package semantic

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// A pathless method routes to its name split by idents (`ListV2Items` → `/list-v2items`).
func TestRegisteredRoutePathlessUsesIdentsKebab(t *testing.T) {
	si := &ServiceInfo{Primary: &ast.ServiceDecl{}}
	cases := map[string]string{
		"ListV2Items":  "/list-v2items",
		"OAuth2Login":  "/o-auth2login",
		"Base64Encode": "/base64encode",
		"GetUser":      "/get-user",
		"ListTodos":    "/list-todos",
	}
	for name, want := range cases {
		got := si.registeredRoute(&ast.Method{Name: name})
		if got != want {
			t.Errorf("registeredRoute pathless %q = %q, want %q (idents canonical)", name, got, want)
		}
	}
}

func TestBasePathFormatOK(t *testing.T) {
	cases := []string{"", "/", "/v1", "/api/v1"}
	for _, bp := range cases {
		_, diags := analyzeWith(parseFiles(t, `service S {}`), Options{BasePath: bp})
		if findCode(diags, CodePathBaseFormat) != nil {
			t.Errorf("basePath %q should be OK, got %v", bp, codes(diags))
		}
	}
}

func TestBasePathFormatRejectsMissingSlash(t *testing.T) {
	_, diags := analyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "v1"})
	d := findCode(diags, CodePathBaseFormat)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity, got %v", d.Severity)
	}
}

func TestBasePathFormatRejectsTrailingSlash(t *testing.T) {
	_, diags := analyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1/"})
	if findCode(diags, CodePathBaseFormat) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// A malformed basePath is reported once per project, not once per package.
func TestBasePathFormatReportedOnce(t *testing.T) {
	files := parseFiles(t, "package a\ntype A { id string }", "package b\ntype B { id string }")
	_, diags := AnalyzeProject(files, Options{BasePath: "v1"})
	if got := codes(diags); !slices.Equal(got, []string{CodePathBaseFormat}) {
		t.Errorf("want one %s, got %v", CodePathBaseFormat, diags)
	}
}

// A basePath problem is reported once, on the manifest, naming its key.
func TestBasePathDiagnosticsNameTheManifest(t *testing.T) {
	root := t.TempDir()
	for basePath, c := range map[string]struct{ code, msg string }{
		"/t/{a-b}/{a-b}": {CodeRoutePattern, `openapi.basePath "/t/{a-b}/{a-b}": net/http's ServeMux refuses the segment "{a-b}"`},
		"/t/{id}/{id}":   {CodeDuplicatePathVar, `openapi.basePath "/t/{id}/{id}" repeats the path variable {id}`},
		"v1":             {CodePathBaseFormat, `openapi.basePath "v1" is malformed`},
	} {
		_, diags := analyzeWith(parseFiles(t, "package app\ntype R { ok bool }"), Options{BasePath: basePath, DesignRoot: root})
		if got := codes(diags); !slices.Equal(got, []string{c.code}) {
			t.Errorf("%s: want one %s, got %v", basePath, c.code, diags)
			continue
		}
		d := diags[0]
		if d.Pos.Filename != filepath.Join(root, "craftgo.design.yaml") || d.Pos.Line != 0 || !strings.Contains(d.Msg, c.msg) {
			t.Errorf("%s: got %s: %s, want the manifest with %q", basePath, d.Pos, d.Msg, c.msg)
		}
	}
}

func TestBasePathFormatRejectsDoubleSlash(t *testing.T) {
	_, diags := analyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1//api"})
	if findCode(diags, CodePathBaseFormat) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPathCollisionAcrossServices(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@prefix("/v1")
service A { get GetUser /users {} }
@prefix("/v1")
service B { get List /users {} }`))
	d := findCode(diags, CodePathCollision)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "GET") || !strings.Contains(d.Msg, "/v1/users") {
		t.Errorf("msg = %q", d.Msg)
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to first declaration, got %+v", d.Related)
	}
}

func TestPathCollisionResolvedViaPrefix(t *testing.T) {
	// A's @prefix and B's inline path both give /v1/users.
	_, diags := Analyze(parseFiles(t, `@prefix("/v1")
service A { get A /users {} }
service B { get B /v1/users {} }`))
	if findCode(diags, CodePathCollision) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPathCollisionWithBasePath(t *testing.T) {
	_, diags := analyzeWith(parseFiles(t,
		`service A { get A /users {} }
service B { get B /users {} }`),
		Options{BasePath: "/api"})
	if findCode(diags, CodePathCollision) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// A basePath variable is a variable of every route, bound once: a request
// field of its name binds to the path both in the checks and in the fields
// a target binds.
func TestBasePathVariableBindsThePath(t *testing.T) {
	opts := Options{BasePath: "/t/{tenant}"}
	pkg, diags := analyzeWith(parseFiles(t, `package app
type Req { tenant string  q string? }
service S { get A /a { request Req } }`), opts)
	expectNoDiags(t, diags)
	m := pkg.Services["S"].Methods[0]
	for _, rf := range RequestFields(m, pkg, PackageResolver(pkg), nil) {
		if rf.Field.Name == "tenant" && rf.Binding != wire.BindPath {
			t.Errorf("tenant binds to %s, want path", rf.Binding)
		}
	}
	_, diags = analyzeWith(parseFiles(t, `package app
type Req { tenant string? }
service S { get A /a { request Req } }`), opts)
	if d := findCode(diags, CodeDecoratorConflict); d == nil || !strings.Contains(d.Msg, "auto-binds to the path segment {tenant}") {
		t.Errorf("want an optional basePath variable refused as a path segment, got %v", diags)
	}
}

// muxRefuses reports whether a real net/http ServeMux panics registering
// pattern.
func muxRefuses(pattern string) (refused bool) {
	defer func() { refused = recover() != nil }()
	http.NewServeMux().Handle(pattern, http.NotFoundHandler())
	return false
}

// A route net/http's ServeMux refuses to register is rejected, and one it
// takes is not; a real mux decides which is which.
func TestRoutePatternsNetHTTPRefusesRejected(t *testing.T) {
	cases := []struct {
		label, basePath, src string
		code                 string // "" for a route the mux takes
	}{
		{"variable inside a segment", "", `@prefix("/org-{org}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"text after a variable", "", `@prefix("/{org}x") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"rest before the end", "", `@prefix("/files/{rest...}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"dollar before the end", "", `@prefix("/x/{$}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"dot-dot segment", "", `@prefix("/v1/../v2") service S { get A /a {} }`, CodeRoutePattern},
		{"dot segment", "", `@prefix("/v1/.") service S { get A /a {} }`, CodeRoutePattern},
		{"name not an identifier", "", `@prefix("/{a-b}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"empty name", "", `@prefix("/{}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"name of dots", "", `@prefix("/{...}") service S { @passthrough get A /a {} }`, CodeRoutePattern},
		{"basePath variable inside a segment", "/t-{x}", `service S { get A /a {} }`, CodeRoutePattern},
		{"basePath rest before the end", "/t/{x...}", `service S { get A /a {} }`, CodeRoutePattern},
		{"prefix repeats a variable", "", `@prefix("/{a}/{a}") service S { @passthrough get A /x {} }`, CodeDuplicatePathVar},
		{"basePath variable repeated", "/t/{id}", `type R { id string }
service S { get A /a/{id} { request R } }`, CodeDuplicatePathVar},
		{"extend block repeats the prefix's variable", "", `type R { id string }
@prefix("/o/{id}") service S {}
extend service S { get A /x/{id} { request R } }`, CodeDuplicatePathVar},
		{"rest ending the route", "", `@prefix("/files/{rest...}") service S { @passthrough get A / {} }`, ""},
		{"prefix variable", "/t/{tenant}", `type R { tenant string  org string }
@prefix("/orgs/{org}") service S { get A /a { request R } }`, ""},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			pkg, diags := analyzeWith(parseFiles(t, "package app\n"+c.src), Options{BasePath: c.basePath})
			si := pkg.Services["S"]
			rt := si.registeredRoute(si.Methods[0])
			if refused := muxRefuses("GET " + rt); refused != (c.code != "") {
				t.Fatalf("net/http refuses %q: %v, want %v", rt, refused, c.code != "")
			}
			if c.code == "" {
				expectNoDiags(t, diags)
				return
			}
			if d := findCode(diags, c.code); d == nil || d.Severity != lexer.SeverityError {
				t.Errorf("want a %s error for route %q, got %v", c.code, rt, diags)
			}
		})
	}
}

func TestPathDifferentVerbNoCollision(t *testing.T) {
	mustClean(t, `service A {
	get GetUser /users {}
	post CreateUser /users {}
}`)
}

func TestPathlessMethodsNoFalseCollision(t *testing.T) {
	// Pathless methods route to `/ping` and `/health`, so they do not collide.
	mustClean(t, `service S {
	get Ping {}
	get Health {}
}`)
}

func TestPathSameServiceDuplicateHandledByOtherCheck(t *testing.T) {
	// A same-service duplicate is reported as a duplicate route only.
	_, diags := Analyze(parseFiles(t, `service S {
	get A /users {}
	get B /users {}
}`))
	if findCode(diags, CodeServiceDuplicateRoute) == nil {
		t.Fatalf("expected service/duplicate-route, got %v", codes(diags))
	}
	if findCode(diags, CodePathCollision) != nil {
		t.Errorf("path/collision should not double-fire, got %v", codes(diags))
	}
}

// Routes that differ only in path variable names collide, in one service or across services.
func TestPathParamRenameStillCollides(t *testing.T) {
	t.Run("same service", func(t *testing.T) {
		_, diags := Analyze(parseFiles(t, `type R {}
service S {
	get A /products/{id} { response R }
	get B /products/{id1} { response R }
}`))
		if findCode(diags, CodeServiceDuplicateRoute) == nil {
			t.Fatalf("rename-bypass must surface as duplicate-route; got %v", codes(diags))
		}
	})
	t.Run("cross service", func(t *testing.T) {
		_, diags := Analyze(parseFiles(t, `type R {}
service A { get GetX /items/{id} { response R } }
service B { get GetY /items/{itemId} { response R } }`))
		if findCode(diags, CodePathCollision) == nil {
			t.Fatalf("rename-bypass must surface across services; got %v", codes(diags))
		}
	})
	t.Run("nested params", func(t *testing.T) {
		_, diags := Analyze(parseFiles(t, `type R {}
service A { get GetX /u/{u}/o/{o} { response R } }
service B { get GetY /u/{userId}/o/{orderId} { response R } }`))
		if findCode(diags, CodePathCollision) == nil {
			t.Fatalf("nested-param rename must collide; got %v", codes(diags))
		}
	})
	t.Run("literal vs param does NOT collide", func(t *testing.T) {
		// net/http prefers the literal segment, so the two routes coexist.
		mustClean(t, `type R {}
type Req { id string }
service A { get GetX /products/abc { response R } }
service B { get GetY /products/{id} { request Req  response R } }`)
	})
}

func TestPathParamMatchesField(t *testing.T) {
	mustClean(t, `type Req { id string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

func TestPathParamWithExplicitDecorator(t *testing.T) {
	mustClean(t, `type Req { rawId string @path("id") }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

func TestPathParamMissing(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Req { name string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`))
	d := findCode(diags, CodePathParamMissing)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "{id}") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestPathParamOrphan(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Req { id string @path("foo") }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`))
	d := findCode(diags, CodePathParamOrphan)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, `"foo"`) {
		t.Errorf("msg = %q", d.Msg)
	}
}

// A path variable is reported missing when the method has no request type.
func TestPathParamMissingWithoutRequest(t *testing.T) {
	expectError(t, `service S {
	get GetUser /users/{id} {}
}`, CodePathParamMissing)
}

// A passthrough method reads path values off the raw request and needs no request type.
func TestPathParamPassthroughNeedsNoRequest(t *testing.T) {
	mustClean(t, `service S {
	@passthrough
	get Stream /users/{id}/feed {}
}`)
}

func TestPathParamSkippedForUnknownRequestType(t *testing.T) {
	// The unknown type is reported elsewhere; the path check skips it.
	_, diags := Analyze(parseFiles(t, `service S {
	get GetUser /users/{id} {
		request   Mystery
	}
}`))
	if findCode(diags, CodePathParamMissing) != nil {
		t.Errorf("unknown req type should not produce path/param-missing, got %v", codes(diags))
	}
}

func TestHealthConflict(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	get HealthCheck /healthz {}
}`))
	d := findCode(diags, CodePathHealthConflict)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "/healthz") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestHealthConflictReadyz(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	get Ready /readyz {}
}`))
	if findCode(diags, CodePathHealthConflict) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestHealthConflictRespectsCustomList(t *testing.T) {
	files := parseFiles(t, `service S {
	get Health /healthz {}
}`)
	_, diags := analyzeWith(files, Options{HealthPaths: []string{"/_status"}})
	if findCode(diags, CodePathHealthConflict) != nil {
		t.Errorf("/healthz should not conflict when HealthPaths overrides it, got %v", codes(diags))
	}
}

func TestHealthConflictNonHealthPath(t *testing.T) {
	mustClean(t, `service S {
	get Status /status {}
}`)
}

// No basePath, no prefix and no path route a method to `/<kebab name>`.
func TestRegisteredRouteFallbackName(t *testing.T) {
	got := (&ServiceInfo{}).registeredRoute(&ast.Method{Name: "Ping"})
	if got != "/ping" {
		t.Errorf("got %q, want %q", got, "/ping")
	}
}

func TestRegisteredRouteIgnoresGroup(t *testing.T) {
	// @group shapes the output folders, not the route.
	pkg, diags := analyzeWith(parseFiles(t, `@prefix("/v1")
@group("admin")
service S { get GetUser /users {} }`), Options{})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	si := pkg.Services["S"]
	got := si.registeredRoute(si.Methods[0])
	if got != "/v1/users" {
		t.Errorf("got %q, want %q", got, "/v1/users")
	}
}

func TestRequestPathFieldsNilGuards(t *testing.T) {
	a := newTestAnalyzer(&Package{Types: map[string]*ast.TypeDecl{}})
	if got := a.requestPathFields(&ast.Method{}, nil); got != nil {
		t.Error("nil request should return nil")
	}
	got := a.requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{"shared", "Req"}},
	}}, nil)
	if got != nil {
		t.Error("unresolved qualified ref should return nil")
	}
	got = a.requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{Name: nil}}, nil)
	if got != nil {
		t.Error("nil Name should return nil")
	}
}

// A path segment binds a field the request promotes from a mixin.
func TestRequestPathFieldsSkipsMixin(t *testing.T) {
	mustClean(t, `type Base { id string }
type Req { Base  name string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

// registeredRoute adds the leading slash a basePath lacks.
func TestRegisteredRouteBasePathMissingSlash(t *testing.T) {
	got := (&ServiceInfo{basePath: "v1"}).registeredRoute(&ast.Method{Name: "Ping"})
	if got != "/v1/ping" {
		t.Errorf("got %q, want %q", got, "/v1/ping")
	}
}

// The request field walk stops on a mixin cycle and still finds the reachable fields.
func TestRequestPathFieldsCyclicMixin(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Types: map[string]*ast.TypeDecl{
			"A": {
				Name: "A",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"B"}}}},
				},
			},
			"B": {
				Name: "B",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"A"}}}},
					&ast.Field{Name: "id"},
				},
			},
		},
	})
	out := a.requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"A"}}}}, []string{"id"})
	if !out.has("id") {
		t.Error("cyclic mixin should still surface reachable fields once")
	}
}

// A mixin from an unknown package is skipped; the host's own fields still count.
func TestRequestPathFieldsUnknownPackageMixin(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Types: map[string]*ast.TypeDecl{
			"A": {
				Name: "A",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Foo"}}}},
					&ast.Field{Name: "id"},
				},
			},
		},
	})
	out := a.requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"A"}}}}, []string{"id"})
	if !out.has("id") {
		t.Error("qualified mixin unresolvable in-package should be skipped, own fields still surface")
	}
}

func TestCheckMethodPathParamsNilName(t *testing.T) {
	a := newTestAnalyzer(&Package{Types: map[string]*ast.TypeDecl{}})
	a.checkMethodPathParams("S", &ast.Method{
		Name:    "M",
		Pos:     lexer.Position{Line: 1},
		Request: &ast.NamedTypeRef{Name: nil},
	}, nil, "/users")
	if len(a.diags) != 0 {
		t.Errorf("nil request name should not diag, got %v", a.diags)
	}
}

func TestPathSetHasNil(t *testing.T) {
	var s *pathParamSet
	if s.has("x") {
		t.Error("nil set should not have anything")
	}
}

// A field named like a path segment but bound to @query leaves the segment unbound.
func TestWireBoundFieldDoesNotCoverPathSegment(t *testing.T) {
	src := `package p
type R { id string @query }
type Resp { x string }
service S { get M /u/{id} { request R  response Resp } }`
	d := expectError(t, src, CodePathParamMissing)
	expectMessage(t, d, "path segment {id} has no matching field")
}

// A generic mixin's field binds a path variable as its type argument's type.
func TestGenericMixinFieldBindsPathVariable(t *testing.T) {
	mustClean(t, `package app
type IdHolder<T> { id T }
type GetReq { IdHolder<string> }
type Resp { ok bool }
@prefix("/things")
service S { get Get /{id} { request GetReq  response Resp } }`)
}

// A type argument resolves where the mixin is written, not in the mixin's package.
func TestCrossPackageGenericMixinFieldBindsPathVariable(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type IdHolder<T> { id T }`,
		"app/a.craftgo": `package app
scalar ThingID string
type GetReq { shared.IdHolder<ThingID> }
type Resp { ok bool }
service S { get Get /things/{id} { request GetReq  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
}

// A trailing {name...} variable is named name: a field of that name, or
// @path("name"), binds it, and @path("name...") names no variable. A {$} is
// no variable.
func TestRestVariableBindsAsItsName(t *testing.T) {
	const head = "package app\ntype Resp { ok bool }\n"
	mustClean(t, head+`type R { rest string }
@prefix("/files/{rest...}")
service S { get Get / { request R  response Resp } }`)
	mustClean(t, head+`type R { p string @path("rest") }
@prefix("/files/{rest...}")
service S { get Get / { request R  response Resp } }`)
	mustClean(t, head+`type R { q string }
@prefix("/root/{$}")
service S { get Get / { request R  response Resp } }`)
	misnamed := head + `type R { p string @path("rest...") }
@prefix("/files/{rest...}")
service S { get Get / { request R  response Resp } }`
	d := expectError(t, misnamed, CodePathParamOrphan)
	expectMessage(t, d, `the variable {rest...} is named "rest"`)
	expectCodeCount(t, misnamed, CodePathParamMissing, 0)
	d = expectError(t, head+`type R { q string }
@prefix("/files/{rest...}")
service S { get Get / { request R  response Resp } }`, CodePathParamMissing)
	expectMessage(t, d, "path segment {rest...} has no matching field")
}

// A route variable the basePath repeats is reported unbound once.
func TestRepeatedPathVariableReportedMissingOnce(t *testing.T) {
	_, diags := analyzeWith(parseFiles(t, "package app\ntype R { q string }\nservice S { get A /a { request R } }"), Options{BasePath: "/t/{id}/{id}"})
	n := 0
	for _, d := range diags {
		if d.Code == CodePathParamMissing {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one %s, got %v", CodePathParamMissing, diags)
	}
}

// A @sensitive field never rides the wire, so a same-named segment stays unbound.
func TestSensitiveFieldDoesNotCoverPathSegment(t *testing.T) {
	d := expectError(t, `package p
type R { id string @sensitive }
service S { get M /users/{id} { request R } }`, CodePathParamMissing)
	expectMessage(t, d, "{id}")
}
