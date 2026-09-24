package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// A pathless method routes to its name split by idents (`ListV2Items` → `/list-v2items`).
func TestResolveMethodPathPathlessUsesIdentsKebab(t *testing.T) {
	a := &analyzer{}
	svc := &ast.ServiceDecl{}
	cases := map[string]string{
		"ListV2Items":  "/list-v2items",
		"OAuth2Login":  "/o-auth2login",
		"Base64Encode": "/base64encode",
		"GetUser":      "/get-user",
		"ListTodos":    "/list-todos",
	}
	for name, want := range cases {
		got := a.resolveMethodPath(svc, &ast.Method{Name: name})
		if got != want {
			t.Errorf("resolveMethodPath pathless %q = %q, want %q (idents canonical)", name, got, want)
		}
	}
}

func TestBasePathFormatOK(t *testing.T) {
	cases := []string{"", "/", "/v1", "/api/v1"}
	for _, bp := range cases {
		_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: bp})
		if findCode(diags, CodePathBaseFormat) != nil {
			t.Errorf("basePath %q should be OK, got %v", bp, codes(diags))
		}
	}
}

func TestBasePathFormatRejectsMissingSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "v1"})
	d := findCode(diags, CodePathBaseFormat)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity, got %v", d.Severity)
	}
}

func TestBasePathFormatRejectsTrailingSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1/"})
	if findCode(diags, CodePathBaseFormat) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestBasePathFormatRejectsDoubleSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1//api"})
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
	_, diags := AnalyzeWith(parseFiles(t,
		`service A { get A /users {} }
service B { get B /users {} }`),
		Options{BasePath: "/api"})
	if findCode(diags, CodePathCollision) == nil {
		t.Fatalf("got %v", codes(diags))
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

func TestSameServiceRouteCollisionStillFlagged(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	get A /users {}
	get B /users {}
}`))
	if findCode(diags, CodeServiceDuplicateRoute) == nil {
		t.Fatalf("got %v", codes(diags))
	}
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
func TestPathParamWarnsWithoutRequest(t *testing.T) {
	expectDiag(t, `service S {
	get GetUser /users/{id} {}
}`, CodePathParamMissing)
}

// A passthrough method reads path values off the raw request and needs no request type.
func TestPathParamPassthroughSkipsWarn(t *testing.T) {
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
	_, diags := AnalyzeWith(files, Options{HealthPaths: []string{"/_status"}})
	if findCode(diags, CodePathHealthConflict) != nil {
		t.Errorf("/healthz should not conflict when HealthPaths overrides it, got %v", codes(diags))
	}
}

func TestHealthConflictNonHealthPath(t *testing.T) {
	mustClean(t, `service S {
	get Status /status {}
}`)
}

func TestResolveMethodPathFallbackName(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
	if got != "/ping" {
		t.Errorf("got %q, want %q", got, "/ping")
	}
}

func TestResolveMethodPathIgnoresGroup(t *testing.T) {
	// @group shapes the output folders, not the route.
	pkg, diags := AnalyzeWith(parseFiles(t, `@prefix("/v1")
@group("admin")
service S { get GetUser /users {} }`), Options{})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	a := newTestAnalyzer(pkg)
	si := pkg.Services["S"]
	got := a.resolveMethodPath(si.Primary, si.Methods[0])
	if got != "/v1/users" {
		t.Errorf("got %q, want %q", got, "/v1/users")
	}
}

func TestResolveMethodPathEmptyParts(t *testing.T) {
	// No basePath, no prefix, no inline path → defaults to /<kebab>.
	a := newTestAnalyzer(&Package{})
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
	if got != "/ping" {
		t.Errorf("got %q, want %q", got, "/ping")
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

// resolveMethodPath adds the leading slash a basePath lacks.
func TestResolveMethodPathBasePathMissingSlash(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.opts.BasePath = "v1"
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
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
	}, "/users")
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
	diags := analyzeOneFile(t, src)
	if len(diags) == 0 {
		t.Fatalf("expected a path-coverage diagnostic for the diverted {id} field")
	}
	if !hasDiagContaining(diags, "path segment") && !hasDiagContaining(diags, "no matching field") {
		t.Errorf("expected path-coverage reject, got: %v", diags)
	}
}

// A @sensitive field never rides the wire, so a same-named segment stays unbound.
func TestSensitiveFieldDoesNotCoverPathSegment(t *testing.T) {
	d := expectError(t, `package p
type R { id string @sensitive }
service S { get M /users/{id} { request R } }`, CodePathParamMissing)
	expectMessage(t, d, "{id}")
}
